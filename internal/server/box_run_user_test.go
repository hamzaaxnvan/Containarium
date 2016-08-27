package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/coderun/engine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// runSh executes script with `sh -c`, with chown stubbed out so the script can
// run as an ordinary user in a test sandbox (the real one runs as root in a box).
func runSh(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", "chown() { :; }\n"+script).CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s\n--- script ---\n%s", err, out, script)
	}
	return string(out)
}

func TestBoxRunScripts_ParseAsShell(t *testing.T) {
	stage, err := boxRunStageScript("/home/alice", "/home/ctn-run")
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"provision": boxRunProvisionScript(),
		"dir":       boxRunDirScript("/tmp/agent-box"),
		"stage":     stage,
	} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("%s script does not parse: %v\n%s", name, err, out)
		}
	}
}

func TestBoxRunProvisionScript_NeverPrivileged(t *testing.T) {
	s := boxRunProvisionScript()
	for _, want := range []string{
		"id -u", // idempotent: only creates when absent
		"useradd --create-home",
		"passwd -l",  // no password login
		"chmod 0700", // private home
		"gpasswd -d", // out of root-equivalent groups
		"sudo wheel", // the groups that matter
		"docker podman",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("provision script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "sudoers") || strings.Contains(s, "NOPASSWD") {
		t.Errorf("provisioning must never grant sudo:\n%s", s)
	}
}

func TestBoxRunStageScript_RefusesUnsafeOrEqualHomes(t *testing.T) {
	for _, tc := range []struct{ owner, run string }{
		{"/home/alice", "/home/alice"},            // same home: would stage onto itself
		{"/home/al ice", "/home/ctn-run"},         // space
		{"/home/alice;rm -rf /", "/home/ctn-run"}, // metacharacters
		{"home/alice", "/home/ctn-run"},           // relative
		{"/home/../etc", "/home/ctn-run"},         // traversal
		{"/home/alice", "/home/ctn|run"},          // sed delimiter
		{"/home/alice/", "/home/ctn-run"},         // trailing slash
		{"", "/home/ctn-run"},
	} {
		if _, err := boxRunStageScript(tc.owner, tc.run); err == nil {
			t.Errorf("stage(%q, %q) must be refused", tc.owner, tc.run)
		}
	}
}

// The stage script, run for real on two temp homes: the platform's own box
// credential arrives 0600, config has the owner's home rewritten to the run
