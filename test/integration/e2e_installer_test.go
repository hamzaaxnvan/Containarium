package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2EInstallerQuickstart is the installer-level (Layer 3) e2e for
// `install.sh --quickstart`. It provisions a BARE Ubuntu 24.04 VM (not the
// containarium terraform module, which pre-installs the daemon), copies the
// tree's hacks/install.sh onto it, and runs the fresh-VM bootstrap:
//
//	sudo bash install.sh --quickstart alice --stack nodejs
//
// then asserts the daemon installed and the first box actually came up. This
// exercises the keyless path — the headline "skip bring-your-own-ssh-key"
// behavior — so it needs no key material and no SSH-into-the-box.
//
// Gated like the other GCP e2e tests: skips in -short and without GCP_PROJECT.
//
//	GCP_PROJECT=my-project GCP_ZONE=asia-east1-a \
//	  go test -v -run TestE2EInstallerQuickstart -timeout 40m ./test/integration/
//
// Env knobs: GCP_ZONE (default asia-east1-a), E2E_MACHINE_TYPE (default
// n2-standard-4), E2E_INSTALL_SCRIPT (default ../../hacks/install.sh),
// KEEP_INSTANCE=true to leave the VM for debugging.
func TestE2EInstallerQuickstart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	project := os.Getenv("GCP_PROJECT")
	if project == "" {
		t.Skip("GCP_PROJECT not set, skipping installer E2E test")
	}

	zone := envOr("GCP_ZONE", "asia-east1-a")
	machine := envOr("E2E_MACHINE_TYPE", "n2-standard-4")
	script := envOr("E2E_INSTALL_SCRIPT", "../../hacks/install.sh")
	scriptAbs, err := filepath.Abs(script)
	require.NoError(t, err)
	require.FileExists(t, scriptAbs, "install.sh not found; set E2E_INSTALL_SCRIPT")

	// Unique per run so parallel/retried runs don't collide; cleaned up below.
	instance := fmt.Sprintf("qs-installer-e2e-%d", time.Now().Unix())
	const box = "alice"

	// The whole flow (provision + apt + Incus + a box with a stack) is slow.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	// ── Provision a bare Ubuntu VM ──────────────────────────────────────
	t.Logf("Creating bare Ubuntu VM %q (%s, %s)...", instance, machine, zone)
