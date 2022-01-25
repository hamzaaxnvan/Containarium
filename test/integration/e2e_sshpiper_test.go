package integration

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2ESSHPiperSetup verifies the sshpiper-based SSH proxy is correctly deployed.
// This test runs against a live sentinel + spot VM deployment.
//
// Required env vars:
//
//	GCP_PROJECT    — GCP project ID
//	GCP_ZONE       — zone of the sentinel VM
//	SENTINEL_VM    — name of the sentinel VM instance
//	SPOT_VM        — name of the spot VM instance
//
// Run: go test -v -run TestE2ESSHPiper -tags=integration ./test/integration/
func TestE2ESSHPiperSetup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	project := os.Getenv("GCP_PROJECT")
	zone := os.Getenv("GCP_ZONE")
	sentinelVM := os.Getenv("SENTINEL_VM")
	spotVM := os.Getenv("SPOT_VM")

	if project == "" || zone == "" || sentinelVM == "" || spotVM == "" {
		t.Skip("GCP_PROJECT, GCP_ZONE, SENTINEL_VM, and SPOT_VM must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Helper: run command on sentinel via IAP (port 2222)
	sshSentinel := func(t *testing.T, cmd string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "gcloud", "compute", "ssh", sentinelVM,
			"--project="+project,
			"--zone="+zone,
			"--tunnel-through-iap",
			"--ssh-flag=-p 2222",
			"--command="+cmd,
		).CombinedOutput()
		if err != nil {
			t.Logf("SSH to sentinel failed: %v\nOutput: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Helper: run command on spot VM via IAP
	sshSpot := func(t *testing.T, cmd string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "gcloud", "compute", "ssh", spotVM,
			"--project="+project,
			"--zone="+zone,
			"--tunnel-through-iap",
			"--command="+cmd,
		).CombinedOutput()
		if err != nil {
			t.Logf("SSH to spot VM failed: %v\nOutput: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	t.Run("SentinelSSHDOnPort2222Only", func(t *testing.T) {
		// Verify sshd is NOT on port 22 and IS on port 2222
		output := sshSentinel(t, "sudo ss -tlnp | grep sshd")
		t.Logf("sshd listening:\n%s", output)

		assert.Contains(t, output, ":2222", "sshd should listen on port 2222")
		// Port 22 should be owned by sshpiper, not sshd
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, ":22 ") && strings.Contains(line, "sshd") {
				// Check it's not :22 (could be :2222 which is fine)
				if strings.Contains(line, "*:22 ") || strings.Contains(line, "0.0.0.0:22 ") {
					t.Error("sshd should NOT listen on port 22 (owned by sshpiper)")
				}
			}
		}
	})

	t.Run("SSHPiperRunning", func(t *testing.T) {
		output := sshSentinel(t, "systemctl is-active sshpiper")
		assert.Equal(t, "active", output, "sshpiper service should be active")
	})

	t.Run("SSHPiperOnPort22", func(t *testing.T) {
		output := sshSentinel(t, "sudo ss -tlnp | grep ':22 '")
		t.Logf("Port 22 listener:\n%s", output)
		assert.Contains(t, output, "sshpiperd", "sshpiper should be listening on port 22")
	})

	t.Run("SSHPiperConfigExists", func(t *testing.T) {
		output := sshSentinel(t, "cat /etc/sshpiper/config.yaml")
		t.Logf("sshpiper config:\n%s", output)

		require.NotEmpty(t, output, "sshpiper config should exist")
		assert.Contains(t, output, "version:", "config should have version")
		assert.Contains(t, output, "pipes:", "config should have pipes section")
	})

	t.Run("SSHPiperHostKeyExists", func(t *testing.T) {
		output := sshSentinel(t, "ls -la /etc/sshpiper/host_key /etc/sshpiper/upstream_key 2>&1")
		t.Logf("Keys:\n%s", output)
		assert.Contains(t, output, "host_key", "host key should exist")
		assert.Contains(t, output, "upstream_key", "upstream key should exist")
	})

	t.Run("Port22NotInDNAT", func(t *testing.T) {
		output := sshSentinel(t, "sudo iptables -t nat -L SENTINEL_PREROUTING -n 2>/dev/null || echo 'no chain'")
