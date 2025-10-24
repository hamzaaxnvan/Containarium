package container

import (
	"errors"
	"testing"
)

// stubEnsureJumpAccount replaces the host-side ensure seam for one test.
// Not parallel-safe (package-level seam), same as stubJumpAccount.
func stubEnsureJumpAccount(t *testing.T, ensure func(username string) error) {
	t.Helper()
	prev := ensureJumpServerAccountFn
	ensureJumpServerAccountFn = ensure
	t.Cleanup(func() { ensureJumpServerAccountFn = prev })
