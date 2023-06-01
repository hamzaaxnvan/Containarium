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
	RunE: runCloudLogin,
}

var (
	cloudEnrollControlPlane  string
	cloudEnrollTokenFile     string
	cloudEnrollInsecure      bool
	cloudEnrollJWTSecretFile string
	cloudEnrollBackendID     string
	cloudEnrollNoDriverToken bool
	cloudEnrollDriverTTL     time.Duration
	cloudEnrollAdoptForeign  bool
)

var cloudEnrollCmd = &cobra.Command{
	Use:   "enroll",
	Short: "Self-register this host with a single-use join token (BYO compute)",
	Long: `Redeem a single-use join token to register this host with a cloud control
plane, then write ~/.containarium/cloud.yaml.

Unlike 'cloud login' (which takes a sysadmin-issued host-id + token out of
band), 'cloud enroll' is the self-service BYO flow: the token from the cloud's
"Add compute" one-liner is redeemed against the control plane (EnrollHost),
which returns the host id and registers the host. The same token becomes this
host's durable bearer. The token is read from --token-file so it never lands
in shell history.`,
	RunE: runCloudEnroll,
}

var cloudStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show this host's cloud enrollment",
	RunE:  runCloudStatus,
}

var cloudLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove this host's enrollment (~/.containarium/cloud.yaml)",
	Long: `Delete the local enrollment config. The daemon stops actuating on next
restart. The cloud-side host row stays until a sysadmin tombstones it
(cloud DeleteHost).`,
	RunE: runCloudLogout,
}

func init() {
	rootCmd.AddCommand(cloudCmd)
	cloudCmd.AddCommand(cloudLoginCmd, cloudEnrollCmd, cloudStatusCmd, cloudLogoutCmd)

	cloudLoginCmd.Flags().StringVar(&cloudControlPlane, "control-plane", "", "cloud control-plane gRPC address (host:port) (required)")
	cloudLoginCmd.Flags().StringVar(&cloudHostID, "host-id", "", "cloud-assigned host UUID from the sysadmin (required)")
	cloudLoginCmd.Flags().StringVar(&cloudTokenFile, "token-file", "", "file containing the host bearer token (required)")
	_ = cloudLoginCmd.MarkFlagRequired("control-plane")
	_ = cloudLoginCmd.MarkFlagRequired("host-id")
	_ = cloudLoginCmd.MarkFlagRequired("token-file")

	cloudEnrollCmd.Flags().StringVar(&cloudEnrollControlPlane, "control-plane", "", "cloud control-plane gRPC address (host:port) (required)")
	cloudEnrollCmd.Flags().StringVar(&cloudEnrollTokenFile, "token-file", "", "file containing the single-use join token (required)")
	cloudEnrollCmd.Flags().BoolVar(&cloudEnrollInsecure, "insecure", false, "dial the control plane without TLS (local dev only)")
	cloudEnrollCmd.Flags().StringVar(&cloudEnrollJWTSecretFile, "jwt-secret-file", defaultDaemonJWTSecretFile, "path to this daemon's JWT secret; used to mint the BYOC driver token the cloud replays to drive this host")
