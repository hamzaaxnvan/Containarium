package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// `containarium tracker dispatch` / `tracker dispatches` (#2022).

func TestTrackerDispatch_OnceAndInterval(t *testing.T) {
	t.Run("--once returns after one tick with its error", func(t *testing.T) {
		calls := 0
		want := errors.New("tick failed")
		err := runDispatchLoop(context.Background(), true, time.Hour, func() error { calls++; return want }, func(string, ...any) {})
		if calls != 1 || !errors.Is(err, want) {
			t.Fatalf("calls = %d, err = %v; want 1 call returning the tick error", calls, err)
		}
	})

	t.Run("--interval loops until ctx cancel, surviving tick errors", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls, logged := 0, 0
		err := runDispatchLoop(ctx, false, time.Millisecond, func() error {
			calls++
			if calls == 3 {
				cancel()
			}
			return errors.New("transient forge error")
		}, func(string, ...any) { logged++ })
		if err != nil {
			t.Fatalf("loop err = %v, want nil on cancel", err)
		}
		if calls != 3 {
			t.Fatalf("calls = %d, want 3 (loop kept going after errors, stopped on cancel)", calls)
		}
		if logged < 2 {
			t.Errorf("logged = %d, want the tick errors reported", logged)
		}
	})
}

func TestValidateDispatchFlags(t *testing.T) {
	tests := []struct {
		name            string
		once            bool
		interval        time.Duration
		intervalChanged bool
		maxStarts       int32
		wantErr         string
	}{
		{name: "default loop", interval: 60 * time.Second},
		{name: "once", once: true, interval: 60 * time.Second},
		{name: "explicit interval", interval: 5 * time.Minute, intervalChanged: true},
		{name: "once with interval", once: true, interval: time.Minute, intervalChanged: true, wantErr: "mutually exclusive"},
		{name: "interval too short", interval: 100 * time.Millisecond, intervalChanged: true, wantErr: "at least"},
		{name: "max starts", once: true, interval: time.Minute, maxStarts: 2},
		{name: "negative max starts", once: true, interval: time.Minute, maxStarts: -1, wantErr: "--max-starts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDispatchFlags(tt.once, tt.interval, tt.intervalChanged, tt.maxStarts)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseDispatchStateFlag(t *testing.T) {
	tests := []struct {
		in      string
		want    pb.TrackerDispatchState
		wantErr bool
	}{
		{"", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_UNSPECIFIED, false},
		{"queued", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_QUEUED, false},
		{"RUNNING", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING, false},
		{"done", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_DONE, false},
		{"failed", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED, false},
		{"unspecified", 0, true},
		{"stuck", 0, true},
	}
	for _, tt := range tests {
		got, err := parseDispatchStateFlag(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseDispatchStateFlag(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseDispatchStateFlag(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// httpModeStub points the CLI at a stub gateway for one test.
func httpModeStub(t *testing.T, respBody string) (gotMethod, gotPath, gotQuery *string) {
	t.Helper()
	var m, p, q string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, p, q = r.Method, r.URL.Path, r.URL.RawQuery
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	oldServer, oldHTTP, oldToken := serverAddr, httpMode, authToken
	t.Cleanup(func() { serverAddr, httpMode, authToken = oldServer, oldHTTP, oldToken })
	serverAddr, httpMode, authToken = srv.URL, true, "tok"
	return &m, &p, &q
}

