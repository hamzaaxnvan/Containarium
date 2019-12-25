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
