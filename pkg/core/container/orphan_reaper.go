package container

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	orphanReaperInterval    = 5 * time.Minute
	orphanReaperHomeRoot    = "/home"
	orphanReaperGracePeriod = 30 * time.Second // delay before first sweep to avoid racing startup
)

// RunOrphanReaper periodically removes host-user accounts whose container no
// longer exists.  A deleted container whose `userdel -r` failed (lock
// contention with google-guest-agent on GCP) leaves a stale `/home/<user>`
// dir with an authorized_keys.  ServeAuthorizedKeys already filters these
// from the keysync response, but without cleanup the dirs accumulate and the
// O(orphans) walk + ssh-keygen per tick widens the #830 first-connect race.
//
// containerExistsFn returns true when a live container exists for username.
// Blocks until ctx is cancelled.
func RunOrphanReaper(ctx context.Context, containerExistsFn func(username string) bool) {
	RunOrphanReaperWithRoot(ctx, orphanReaperHomeRoot, containerExistsFn)
}

// RunOrphanReaperWithRoot is the testable variant that accepts an explicit homeRoot.
func RunOrphanReaperWithRoot(ctx context.Context, homeRoot string, containerExistsFn func(username string) bool) {
	// Grace period: let the daemon finish startup before first sweep.
	select {
	case <-ctx.Done():
		return
	case <-time.After(orphanReaperGracePeriod):
	}

	reapOnce(homeRoot, containerExistsFn, userShell, DeleteJumpServerAccount)

	ticker := time.NewTicker(orphanReaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reapOnce(homeRoot, containerExistsFn, userShell, DeleteJumpServerAccount)
		}
	}
}

// UserLoginShell exports userShell for packages outside pkg/core/container
// (e.g. the keysync HTTP handler) that need the exact same
// orphan-reaper-eligibility check without duplicating the getent logic —
// see IsContainerManagedShell.
func UserLoginShell(username string) (string, error) {
	return userShell(username)
}
