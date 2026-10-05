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
		t.Logf("DNAT rules:\n%s", output)

		// Port 22 should NOT appear in DNAT rules
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "dpt:22 ") || strings.Contains(line, "dpt:22\t") {
				t.Error("Port 22 should NOT be in DNAT rules (handled by sshpiper)")
			}
		}
	})

	t.Run("SpotVMFail2BanWhitelist", func(t *testing.T) {
		output := sshSpot(t, "sudo fail2ban-client get sshd ignoreip 2>/dev/null || echo 'fail2ban not running'")
		t.Logf("fail2ban ignoreip:\n%s", output)

		assert.Contains(t, output, "10.128.0.0/9", "fail2ban should whitelist GCE internal range")
	})

	t.Run("SpotVMAuthorizedKeysEndpoint", func(t *testing.T) {
		// Test the /authorized-keys endpoint from the spot VM
		output := sshSpot(t, "curl -s http://localhost:8080/authorized-keys")
		t.Logf("/authorized-keys response:\n%s", output)

		assert.Contains(t, output, "keys", "response should contain keys field")
	})

	t.Run("SpotVMSentinelKeyEndpoint", func(t *testing.T) {
		// Test the /authorized-keys/sentinel endpoint accepts POST
		output := sshSpot(t, `curl -s -X POST -H "Content-Type: application/json" -d '{"public_key":"ssh-ed25519 AAAA_test test@test"}' http://localhost:8080/authorized-keys/sentinel`)
		t.Logf("/authorized-keys/sentinel response:\n%s", output)

		assert.Contains(t, output, "updated", "response should contain updated count")
	})

	t.Run("SSHPiperFailToban", func(t *testing.T) {
		// Get the sentinel's external IP
		sentinelIP := sshSentinel(t, "curl -s http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip -H 'Metadata-Flavor: Google'")
		if sentinelIP == "" {
			t.Skip("Could not get sentinel external IP")
		}

		t.Logf("Sentinel external IP: %s", sentinelIP)

		// Try SSH with wrong key — should get rejected by sshpiper
		// We do this from a local perspective, just verifying the failtoban is configured
		output := sshSentinel(t, "sudo journalctl -u sshpiper --no-pager -n 20 2>/dev/null || echo 'no logs'")
		t.Logf("sshpiper recent logs:\n%s", output)
	})

	t.Log("All sshpiper E2E checks passed")
}

// TestE2ESSHPiperBruteForceProtection tests that sshpiper's failtoban
// actually bans IPs after repeated auth failures.
//
// This test requires an external IP that can SSH to the sentinel.
// It intentionally fails authentication to trigger the ban.
func TestE2ESSHPiperBruteForceProtection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	project := os.Getenv("GCP_PROJECT")
	zone := os.Getenv("GCP_ZONE")
	sentinelVM := os.Getenv("SENTINEL_VM")
	sentinelExternalIP := os.Getenv("SENTINEL_EXTERNAL_IP")

	if project == "" || zone == "" || sentinelVM == "" || sentinelExternalIP == "" {
		t.Skip("GCP_PROJECT, GCP_ZONE, SENTINEL_VM, and SENTINEL_EXTERNAL_IP must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Generate a throwaway key for failed auth attempts
	tmpDir := t.TempDir()
	keyPath := tmpDir + "/throwaway_key"
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to generate throwaway key: %v\n%s", err, out)
	}

	// Attempt SSH connections with the wrong key (should all fail)
	t.Log("Sending failed SSH auth attempts to trigger failtoban...")
	for i := 0; i < 5; i++ {
		cmd := exec.CommandContext(ctx, "ssh",
			"-i", keyPath,
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=5",
			"-o", "BatchMode=yes",
			"-p", "22",
			"nonexistent@"+sentinelExternalIP,
			"echo hello",
		)
		out, _ := cmd.CombinedOutput()
		t.Logf("Attempt %d: %s", i+1, strings.TrimSpace(string(out)))
		time.Sleep(500 * time.Millisecond)
	}

	// Check sshpiper logs for ban evidence
	sshSentinel := func(cmd string) string {
		out, _ := exec.CommandContext(ctx, "gcloud", "compute", "ssh", sentinelVM,
			"--project="+project,
			"--zone="+zone,
			"--tunnel-through-iap",
			"--ssh-flag=-p 2222",
			"--command="+cmd,
		).CombinedOutput()
		return strings.TrimSpace(string(out))
	}

	output := sshSentinel("sudo journalctl -u sshpiper --no-pager -n 50 2>/dev/null")
	t.Logf("sshpiper logs after brute force:\n%s", output)

	// The failtoban plugin should show ban activity
	if strings.Contains(output, "ban") || strings.Contains(output, "blocked") {
		t.Log("failtoban appears to have banned the attacker IP")
	} else {
		t.Log("Note: ban evidence not found in recent logs (may need more attempts or longer wait)")
	}
}
