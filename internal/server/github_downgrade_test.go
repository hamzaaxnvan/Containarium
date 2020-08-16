package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestCheckDowngrade(t *testing.T) {
	cases := []struct {
		name         string
		running, tag string
		allow        bool
		wantRefuse   bool
	}{
		{"older tag refused", "0.118.0", "v0.117.0", false, true},
		{"older tag, allowed", "0.118.0", "v0.117.0", true, false},
		{"equal tag", "0.118.0", "v0.118.0", false, false},
		{"newer tag", "0.118.0", "v0.119.0", false, false},
		{"tag without v prefix, older", "0.118.0", "0.100.0", false, true},
		{"numeric not lexical: 0.9.0 < 0.118.0", "0.118.0", "v0.9.0", false, true},
		{"numeric not lexical: 0.118.0 > 0.99.0 is newer", "0.99.0", "v0.118.0", false, false},
		{"pre-release of running release is older", "0.118.0", "v0.118.0-rc1", false, true},
		{"release is newer than running pre-release", "0.118.0-rc1", "v0.118.0", false, false},
		{"latest is never refused", "0.118.0", "latest", false, false},
		{"unparseable running version (dev build)", "dev", "v0.1.0", false, false},
		{"empty running version", "", "v0.1.0", false, false},
		{"unparseable tag", "0.118.0", "not-a-version", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDowngrade(tc.running, tc.tag, tc.allow)
			if tc.wantRefuse {
				if err == nil {
					t.Fatalf("checkDowngrade(%q, %q, %v) = nil, want refusal", tc.running, tc.tag, tc.allow)
				}
				if !strings.Contains(err.Error(), "--allow-downgrade") {
					t.Errorf("error %q does not name --allow-downgrade", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkDowngrade(%q, %q, %v) = %v, want nil", tc.running, tc.tag, tc.allow, err)
			}
		})
	}
}

func stubRunningVersion(t *testing.T, v string) {
	t.Helper()
	orig := runningVersion
	runningVersion = func() string { return v }
	t.Cleanup(func() { runningVersion = orig })
}

func TestRunGitHubUpgrade_RefusesDowngradeBeforeAnyFetch(t *testing.T) {
	stubRunningVersion(t, "0.118.0")
