//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/spf13/cobra"
)

func TestBuildPprofURL(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		profile string
		seconds int
		debug   int
		gc      bool
		list    bool
		want    string
		wantErr string
	}{
		{
			name: "plain heap", base: "http://sentinel.example:8888", profile: "heap",
			want: "http://sentinel.example:8888/debug/pprof/heap",
		},
		{
			name: "trailing slash on base is not doubled", base: "http://sentinel.example:8888/", profile: "heap",
			want: "http://sentinel.example:8888/debug/pprof/heap",
		},
		{
			name: "heap with forced GC", base: "https://sentinel.example:8889", profile: "heap", gc: true,
			want: "https://sentinel.example:8889/debug/pprof/heap?gc=1",
		},
		{
			name: "goroutine as text", base: "http://sentinel.example:8888", profile: "goroutine", debug: 1,
			want: "http://sentinel.example:8888/debug/pprof/goroutine?debug=1",
		},
		{
			name: "cpu with duration", base: "http://sentinel.example:8888", profile: "profile", seconds: 45,
			want: "http://sentinel.example:8888/debug/pprof/profile?seconds=45",
		},
		{
			name: "list hits the index, not a named profile", base: "http://sentinel.example:8888", profile: "heap", list: true,
			want: "http://sentinel.example:8888/debug/pprof/",
		},
		{
			name: "empty url", base: "   ", profile: "heap",
			wantErr: "empty --url",
		},
		{
			// A bare host:port is the likeliest operator slip; it must not
			// silently produce a relative path that fails much later.
			name: "missing scheme", base: "sentinel.example:8888", profile: "heap",
			wantErr: "want an http:// or https:// base URL",
		},
		{
			name: "empty profile", base: "http://sentinel.example:8888", profile: "  ",
			wantErr: "empty --profile",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildPprofURL(tc.base, tc.profile, tc.seconds, tc.debug, tc.gc, tc.list)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got URL %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPprofOutputPath(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		profile  string
		debug    int
		list     bool
		want     string
	}{
		{name: "binary profile defaults to a file", profile: "heap", want: "heap.pb.gz"},
		{name: "explicit path always wins", explicit: "/tmp/x.pb.gz", profile: "heap", want: "/tmp/x.pb.gz"},
		{name: "explicit dash stays stdout", explicit: "-", profile: "heap", want: "-"},
		// Text output is meant to be read, so it goes to the terminal — but a
		// binary profile must never default to dumping gzip into a tty.
		{name: "text output defaults to stdout", profile: "goroutine", debug: 1, want: "-"},
		{name: "list defaults to stdout", profile: "heap", list: true, want: "-"},
		{name: "explicit file beats text default", explicit: "dump.txt", profile: "goroutine", debug: 2, want: "dump.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pprofOutputPath(tc.explicit, tc.profile, tc.debug, tc.list); got != tc.want {
				t.Errorf("pprofOutputPath = %q, want %q", got, tc.want)
			}
		})
	}
}

// The client timeout must outlast the CPU profile window, or a --seconds=60
// capture would always be killed by its own client just before finishing.
func TestPprofClientTimeoutOutlastsCPUWindow(t *testing.T) {
	for _, seconds := range []int{1, 30, 60} {
		got := pprofClientTimeout(seconds)
		if got <= time.Duration(seconds)*time.Second {
			t.Errorf("pprofClientTimeout(%d) = %v, must exceed the %ds profile window", seconds, got, seconds)
		}
	}
	if got := pprofClientTimeout(0); got < 30*time.Second {
		t.Errorf("default timeout = %v, want a usable floor for a large heap transfer", got)
	}
}

// TestSentinelPprofEndToEnd drives the real operator path: the cobra command
// signs a request, the real sentinel handler serves a real heap profile behind
// the real HMAC middleware, and the profile lands on disk. The unit tests above
// cover URL assembly; this one covers "does the whole thing actually work".
