package container

import (
	"errors"
	"testing"
	"time"
)

const testSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMw0GUYQZVPWxSAC4T8RdKFGqzb8jxbFFM/SB6ILi1Ji test@host"

// stubJumpAccount replaces the host-side jump-account functions for the
// duration of the test. Not parallel-safe (package-level seam), so the
// tests that use it must not call t.Parallel.
func stubJumpAccount(t *testing.T, create func(username, key string, verbose bool) error) {
	t.Helper()
	prevCreate, prevAdd := createJumpServerAccountFn, addAuthorizedKeyFn
	createJumpServerAccountFn = create
	addAuthorizedKeyFn = func(string, string) error { return nil }
	t.Cleanup(func() {
		createJumpServerAccountFn, addAuthorizedKeyFn = prevCreate, prevAdd
	})
}

// TestCreate_JumpAccountRunsConcurrentlyWithGuestStages pins the overlap
// contract: the host-side jump account must NOT gate the guest stages.
// The stub refuses to finish until WaitForNetwork has been entered — under
// the old sequential order (jump account strictly before the network
// wait) that condition never becomes true, the stub times out, and the
// create fails, failing the test.
func TestCreate_JumpAccountRunsConcurrentlyWithGuestStages(t *testing.T) {
