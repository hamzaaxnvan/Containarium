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
