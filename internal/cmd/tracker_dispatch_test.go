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
