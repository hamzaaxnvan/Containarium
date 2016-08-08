package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseVersionOutput(t *testing.T) {
	cases := []struct {
		name, out, want string
		wantErr         bool
	}{
		{name: "release build", out: "Containarium v0.91.1\n", want: "0.91.1"},
		{name: "dev suffix", out: "Containarium v0.90.1-dev.1\n", want: "0.90.1-dev.1"},
		{name: "leading log noise", out: "some warning\nContainarium v0.91.1\n", want: "0.91.1"},
		{name: "empty", out: "", wantErr: true},
		{name: "unrelated output", out: "usage: foo\n", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVersionOutput(tc.out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseVersionOutput(%q) = %q, want error", tc.out, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseVersionOutput(%q) = %q, %v; want %q", tc.out, got, err, tc.want)
			}
		})
	}
}

// The version probe execs the served binary, so it must run once per distinct
// binary, not once per request.
func TestServedVersionCache_ProbesOncePerChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containariumd")
	if err := os.WriteFile(path, []byte("build-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := &servedVersionCache{probe: func(context.Context, string) (string, error) {
		calls++
		return "0.91.1", nil
	}}

	for i := 0; i < 3; i++ {
		got, err := c.get(context.Background(), path)
		if err != nil || got.Version != "0.91.1" {
			t.Fatalf("get = %+v, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1 for an unchanged binary", calls)
	}

	if err := os.WriteFile(path, []byte("build-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.get(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("probe calls = %d, want 2 after the binary changed", calls)
	}
}

func TestServedVersionCache_ProbeErrorNotCached(t *testing.T) {
	path := filepath.Join(t.TempDir(), "containariumd")
	if err := os.WriteFile(path, []byte("build-a"), 0o600); err != nil {
		t.Fatal(err)
	}
