//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/hostharden"
)

// hostharden is internal plumbing (#1103 fix 3), not a documented top-level
// workflow: `cloud enroll` and `pool join` invoke BlockMetadataFromBridge
// directly, and the persistent systemd unit InstallPersistentUnit writes
// shells out to THIS subcommand (rather than duplicating the iptables/incus
// invocation inline) so the unit and the enrollment-time call can never
// drift apart. Hidden from `containarium --help`; still fully usable if an
// operator needs to re-run it by hand.
var hostHardenCmd = &cobra.Command{
	Use:    "hostharden",
	Short:  "Narrow host-hardening mutations, applied by cloud enroll / pool join and the reboot unit they install",
	Hidden: true,
}
