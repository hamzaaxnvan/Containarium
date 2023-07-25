//go:build !containarium_client

package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/cloud"
	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/hostharden"
)

// defaultDaemonJWTSecretFile aliases the cloud package's shared default so
// the enroll CLI and the daemon-side refresh fallback can never disagree on
// where the secret lives.
const defaultDaemonJWTSecretFile = cloud.DefaultDaemonJWTSecretFile

// cloudCmd is the parent for `containarium cloud <verb>` — host enrollment with
// a cloud control plane (#354, docs/CLOUD-ACTUATION-CLIENT-DESIGN.md). This is
// the OSS opt-in: a registered host receives desired-state container assignments
// + per-org egress network policies from the cloud and reconciles them locally.
//
// Distinct from `containarium login` (which stores a user-facing JWT in
// credentials.json): `cloud login` stores a host bearer in cloud.yaml, used by
// the daemon's actuation client. Slice 1: enrollment config only — the daemon
// heartbeat / WatchAssignments client lands in later slices.
var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Enroll this host with a cloud control plane (actuation)",
	Long: `Manage this host's enrollment with a Containarium cloud control plane.

When enrolled, the daemon runs an actuation client that receives desired-state
container assignments and per-org network policies from the cloud and reconciles
them against local Incus state. Enrollment is opt-in — an unenrolled daemon is
single-tenant and makes no outbound calls.

The host bearer token comes from a cloud sysadmin (who runs the cloud-side
CreateHost); you receive the host ID + token out of band and register here.`,
}

var (
	cloudControlPlane string
	cloudHostID       string
	cloudTokenFile    string
)

var cloudLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Register this host (writes ~/.containarium/cloud.yaml)",
	Long: `Register this host with a cloud control plane.

The token is read from --token-file (not a flag) so it never lands in shell
history. Writes the enrollment to ~/.containarium/cloud.yaml at mode 0600.`,
