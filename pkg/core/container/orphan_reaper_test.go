package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeHomeDir creates homeRoot/username[/.ssh/authorized_keys] for test
// fixtures. withKey=false leaves out the .ssh/authorized_keys file
// entirely, matching a directory the reaper should never consider.
func makeHomeDir(t *testing.T, homeRoot, username string, withKey bool) {
	t.Helper()
	dir := filepath.Join(homeRoot, username)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	if !withKey {
		return
	}
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", sshDir, err)
	}
	akPath := filepath.Join(sshDir, "authorized_keys")
	if err := os.WriteFile(akPath, []byte("ssh-ed25519 AAAA fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", akPath, err)
	}
}

// TestReapOnce_OnlyReapsContainariumManagedShell is the regression test for
// the real incident: an operator's own admin account (real login shell, no
// matching container) must never be reaped just because it lives under
// homeRoot with an authorized_keys file. Only accounts whose shell is
// exactly containerShellPath are eligible.
func TestReapOnce_OnlyReapsContainariumManagedShell(t *testing.T) {
	homeRoot := t.TempDir()

	makeHomeDir(t, homeRoot, "orphaned-tenant", true) // containarium-shell, no container -> reap
	makeHomeDir(t, homeRoot, "admin-user", true)      // /bin/bash, no container -> must NOT reap
