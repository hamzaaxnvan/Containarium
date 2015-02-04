package sentinel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The live harness controls a VM. Exercise its success and failure paths
// against synthetic command-line services before giving it real credentials.
func TestSentinelOutageHarness(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"recover", "PASS"},
		{"slow-boot", "PASS"},
		{"bad-health-baseline", "backend health baseline failed"},
		{"boot-stuck", "backend did not become ready"},
		{"restart", "sentinel restarted"},
		{"restart-count", "sentinel restarted"},
		{"changed-ip", "backend IP changed"},
		{"ssh-stuck", "forwarding did not recover"},
		{"https-stuck", "forwarding did not recover"},
		{"short-outage", "outage must exceed"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			state := filepath.Join(dir, "state")
			require.NoError(t, os.WriteFile(state, []byte("initial"), 0600))
			for _, name := range []string{"gcloud", "ssh", "curl"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(outageCommandFixture), 0700))
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			for k, v := range map[string]string{
				"FIXTURE_STATE": state, "FIXTURE_MODE": tc.mode,
				"GCP_PROJECT": "fixture", "GCP_ZONE": "fixture", "BACKEND_VM": "fixture",
				"SENTINEL_SSH_TARGET": "sentinel", "APP_SSH_TARGET": "app",
				"BACKEND_SSH_TARGET": "backend", "BOOT_TIMEOUT_SECONDS": "2",
				"SENTINEL_HTTPS_IP": "192.0.2.1", "APP_URL": "https://app.example/",
				"EXPECTED_HTTPS_BODY": "fixture-ok", "KEYSYNC_INTERVAL_SECONDS": "1",
				"OUTAGE_SECONDS": "2", "RECOVERY_SECONDS": "2",
			} {
				t.Setenv(k, v)
			}
			if tc.mode == "short-outage" {
				t.Setenv("OUTAGE_SECONDS", "1")
			}
