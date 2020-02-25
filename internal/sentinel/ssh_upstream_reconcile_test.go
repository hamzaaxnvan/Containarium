package sentinel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSSHUpstreamReconcileStagesPolicyChanges never invalidates a running checkpoint.
func TestSSHUpstreamReconcileStagesPolicyChanges(t *testing.T) {
	data, err := os.ReadFile("../../terraform/modules/containarium/scripts/startup-sentinel.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(data), "reconcile_ssh_upstream_control() {")
	if !ok {
		t.Fatal("missing production reconciler")
	}
	body, _, ok := strings.Cut(rest, "\n}\n")
	if !ok {
		t.Fatal("unterminated production reconciler")
	}
	source := "reconcile_ssh_upstream_control() {" + body + "\n}\n"
	source = strings.ReplaceAll(source, "/etc/containarium/ssh-upstream", `"$RECONCILE_TEST_POLICY_DIR"`)
	source = strings.ReplaceAll(source, "/etc/systemd/system/containarium-sentinel.service.d", `"$RECONCILE_TEST_UNIT_DIR"`)
	dir := t.TempDir()
	policyDir, unitDir := filepath.Join(dir, "policy"), filepath.Join(dir, "unit")
	desired, calls, notices := filepath.Join(dir, "desired"), filepath.Join(dir, "calls"), filepath.Join(dir, "notices")
	script := `set -euo pipefail
curl() { cat "$RECONCILE_TEST_DESIRED"; }
systemctl() { printf '%s\n' "$*" >> "$RECONCILE_TEST_CALLS"; }
logger() { printf '%s\n' "$*" >> "$RECONCILE_TEST_NOTICES"; }
` + source + "reconcile_ssh_upstream_control\n"
	run := func() {
