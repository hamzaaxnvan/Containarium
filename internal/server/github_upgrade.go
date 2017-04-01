package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"github.com/footprintai/containarium/internal/releases"
	"github.com/footprintai/containarium/pkg/version"
)

// runningVersion is the running daemon's version; a var so tests can pin it.
var runningVersion = version.GetVersion

// checkDowngrade refuses a tag older than the running version unless
// allowDowngrade is set (#3059). Deliberately conservative: it refuses only
// when BOTH versions parse as semver and tag < running. "latest", an empty or
// non-semver running version (dev builds) and an unparseable tag are never
// refused, because we cannot tell that they are older. Semver ordering applies,
// so a pre-release (v1.2.0-rc1) is older than its release (1.2.0); build
// metadata is ignored.
func checkDowngrade(running, tag string, allowDowngrade bool) error {
	if allowDowngrade || tag == "latest" {
		return nil
	}
	rv, err1 := semver.ParseTolerant(running)
	tv, err2 := semver.ParseTolerant(tag)
	if err1 != nil || err2 != nil {
		return nil
	}
	if tv.LT(rv) {
		return fmt.Errorf("refusing to downgrade: tag %s is older than the running version %s; pass --allow-downgrade (allow_downgrade) to install it deliberately", tag, running)
	}
	return nil
}

// githubUpgradeBinaryName matches selfupdate.go's releaseBinaryName — the
// daemon artifact published alongside SHA256SUMS.txt on every release (#1779).
const githubUpgradeBinaryName = "containariumd-linux-amd64"

// restartAfterGitHubSwap launches the upgrade watchdog and restarts services
// after a successful GitHub-direct binary swap, mirroring the tail of
// AutoUpdater.checkAndUpdate. A package var (not a plain call) so tests can
// stub out the real systemctl/watchdog side effects — see github_upgrade_test.go.
var restartAfterGitHubSwap = func(binaryPath string) {
	(&AutoUpdater{binaryPath: binaryPath}).launchWatchdog()

	go func() {
		time.Sleep(1 * time.Second)
		if exec.Command("systemctl", "is-active", "containarium-tunnel").Run() == nil { // #nosec G204
			log.Printf("[github-upgrade] restarting containarium-tunnel...")
			_ = exec.Command("systemctl", "restart", "containarium-tunnel").Run() // #nosec G204
		}
		log.Printf("[github-upgrade] restarting containarium...")
		if err := exec.Command("systemctl", "restart", "containarium").Run(); err != nil { // #nosec G204
			_ = exec.Command("systemctl", "restart", "containarium-daemon").Run() // #nosec G204
		}
	}()
}

// runGitHubUpgrade downloads containariumd-linux-amd64 for the given GitHub
// release tag directly from GitHub Releases (#1028), verifies it against the
// published SHA256SUMS.txt, smoke-tests it, and atomically swaps it into
// place — the same download/verify/swap sequence AutoUpdater.checkAndUpdate
// uses for the sentinel-served path, but pulling straight from GitHub.
//
// Deliberately NOT sharing checkAndUpdate's body: that method is untested,
// production-critical code every sentinel-connected daemon relies on daily
// (see #1547). Keeping this as its own self-contained function means a bug
// in the GitHub-direct path can't touch the sentinel path, and vice versa.
// Only the risk-free pieces are reused: checksumFile (a pure free function)
// and AutoUpdater.launchWatchdog (via a throwaway struct value, since it
// only reads binaryPath).
//
// A tag older than the running version is refused unless allowDowngrade
// (checkDowngrade), before any network access.
//
// Returns whether the binary was swapped (false only on a non-force no-op
// match against the tag's published checksum). On success the daemon
// restarts via restartAfterGitHubSwap, so the caller should treat a nil
// error with changed=true as "upgrade started."
func runGitHubUpgrade(ctx context.Context, binaryPath, tag string, force, allowDowngrade bool) (bool, error) {
	if err := checkDowngrade(runningVersion(), tag, allowDowngrade); err != nil {
		return false, err
	}
