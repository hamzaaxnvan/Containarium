package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var upgradeForce bool
var upgradeGithubTag string
var upgradeAllowDowngrade bool

var backendsUpgradeCmd = &cobra.Command{
	Use:   "upgrade [backend-id]",
	Short: "Upgrade a backend's daemon to the sentinel-served binary now",
	Long: `Trigger a daemon upgrade on a backend immediately, instead of waiting for the
periodic auto-update tick. By default the daemon pulls the binary the sentinel
currently serves, SHA-verifies it, smoke-tests it (runs 'version'), swaps it in
atomically (keeping the previous binary as .old), and restarts.

With no backend-id it upgrades the local/primary daemon; with a peer id it
forwards to that peer (which upgrades its own daemon). Admin-only.

--github-tag pulls a specific GitHub release tag (e.g. "v0.82.0") directly
from GitHub Releases instead — same verify/smoke-test/swap sequence, but no
sentinel involved. Opt-in; omit it for the default sentinel-served upgrade.
See #1028.

A --github-tag older than the running daemon version is refused unless you
also pass --allow-downgrade (a deliberate rollback). Only a tag and a running
version that both parse as semver can be refused; "latest" and dev builds are
not blocked. See #3059.

The upgrade is asynchronous: on a successful swap the daemon restarts, so this
command returns an upgrade id immediately. Confirm the result with
'containarium backends versions' once the daemon is back. See #354.

Requires --server pointing at the daemon's HTTP address.`,
	Args: cobra.MaximumNArgs(1),
