package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestOwnerFromContainerName(t *testing.T) {
	cases := map[string]struct {
		want   string
		wantOK bool
	}{
		"alice-container":         {"alice", true},
		"bob-with-dash-container": {"bob-with-dash", true},
		"":                        {"", false},
		"   ":                     {"", false},
		"alice":                   {"", false},
		"-container":              {"", false},
		"some-system-svc":         {"", false},
		"caddy":                   {"", false},
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, ok := OwnerFromContainerName(in)
			if got != want.want || ok != want.wantOK {
				t.Fatalf("OwnerFromContainerName(%q) = (%q,%v), want (%q,%v)",
					in, got, ok, want.want, want.wantOK)
			}
		})
	}
}
