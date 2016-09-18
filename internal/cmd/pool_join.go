//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/cloud"
	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/hostharden"
	"github.com/footprintai/containarium/internal/sentinel"
)

// pool join — the turnkey, one-command path that turns a fresh Linux host
// into a member of YOUR pool (prd/oss/byo-compute-pool-join.md). It
// productizes the manual install-lab-*.sh ritual: it ensures the canonical
// hardened daemon unit (reusing the same template `service install` writes —
// no hand-authored, capability-trap-prone unit), drops in the --pool config,
// and writes + starts the tunnel unit that dials the sentinel.
//
// MVP scope: it assumes the binary is already at /usr/local/bin/containariumd
// (#1780; ensureDaemonUnitAndSecret's compat symlink covers the old name) and
// that the operator passes a join token. Deferred to follow-ups (per the
// PRD): the `doctor` capability self-check, scoped short-lived token minting,
// binary fetch/--binary-src, and a --role=tunnel-only variant.

const (
	tunnelUnitPath  = "/etc/systemd/system/containarium-tunnel.service"
	daemonDropInDir = "/etc/systemd/system/containarium.service.d"
	daemonDropIn    = daemonDropInDir + "/pool.conf"
	daemonBinPath   = "/usr/local/bin/containariumd"

	// sentinelAuthSecretFile holds CONTAINARIUM_SENTINEL_AUTH_SECRET for the
	// EnvironmentFile= directive in the pool drop-in (see renderPoolDropIn).
	// Separate from daemonDropIn (which stays 0644/no-secrets by convention,
	// #341) so the fleet-wide HMAC secret this host needs to authenticate
	// the sentinel's keysync/certsync requests never lands in a
	// world-readable file.
	sentinelAuthSecretFile = "/etc/containarium/sentinel-auth.env" // #nosec G101 -- a file PATH, not a credential

	// tunnelTokenSecretFile holds CONTAINARIUM_TUNNEL_TOKEN for the
	// EnvironmentFile= directive in the tunnel unit (see renderTunnelUnit).
	// #935: the tunnel-handshake token used to be baked straight into the
	// unit's world-readable (0644) ExecStart line — visible to any local
	// user via `systemctl cat`/`systemctl show -p ExecStart`/`ps`. It's a
	// bearer-equivalent credential (the tunnel-server's TokenPolicy grants
	// standing tunnel access to whoever presents it), so it gets the same
	// root-only-file treatment sentinelAuthSecretFile already established.
	tunnelTokenSecretFile = "/etc/containarium/tunnel-token.env" // #nosec G101 -- a file PATH, not a credential

	// sentinelAuthSecretDocPath is docs/SENTINEL-AUTH-SECRET.md's own
	// canonical path for a manually-provisioned secret (its "Manual setup"
	// section), distinct from sentinelAuthSecretFile above (what THIS CLI
	// writes when --sentinel-auth-secret is passed). Checked alongside it
	// in sentinelAuthSecretCandidatePaths (#959) so a host provisioned via
	// the documented manual path isn't treated as unprovisioned just
	// because pool join itself never wrote sentinelAuthSecretFile there.
	sentinelAuthSecretDocPath = "/etc/containarium/env.secrets" // #nosec G101 -- a file PATH, not a credential
)

// minimalDaemonArgv is the baseline daemon command used when no existing
// ExecStart can be read (a fresh host). On a host that already runs the daemon
// with extra flags, those flags are preserved instead (see resolvePoolDaemonArgv).
func minimalDaemonArgv() []string {
	return []string{daemonBinPath, "daemon", "--rest", "--jwt-secret-file", "/etc/containarium/jwt.secret"}
}

var (
	poolJoinSentinels          []string
	poolJoinSentinelPins       []string
	poolJoinRegion             string
	poolJoinToken              string
	poolJoinTokenFile          string
	poolJoinPool               string
	poolJoinSpotID             string
	poolJoinPorts              string
	poolJoinPublicHostname     string
	poolJoinPublicPort         int
	poolJoinBaseDomain         string
	poolJoinDryRun             bool
	poolJoinDaemonFlags        []string
	poolJoinCloudControlPlane  string
	poolJoinCloudInsecure      bool
	poolJoinSentinelAuthSecret string
)

var poolJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Turn THIS host into a member of your pool (run on the host, as root)",
	Long: `Join this host to your compute pool in one command. Writes the canonical
hardened daemon unit (same template as 'service install'), a --pool
drop-in, and the tunnel unit that dials your sentinel — then enables and
starts both. Idempotent: re-running re-applies the config.

Run ON the host you're adding, as root. Use --dry-run to print the unit
files without writing anything.

The join token is resolved in this order — first one present wins:
  1. --token-file <path>          (read, trimmed; never lands on argv or in shell history)
  2. $CONTAINARIUM_TUNNEL_TOKEN    (same env var 'containarium tunnel' itself reads)
  3. --token <value>               (kept for interactive use and backward compatibility)

Example:
  sudo containarium pool join \
    --sentinel sentinel.example.com:443 \
    --sentinel-pin sha256:<64 hex> \
    --pool prod \
    --token-file /etc/containarium/join-token \
    --public-hostname node1.example.com --public-port 443

Multi-region (probe + pick the closest sentinel from the host):
  sudo containarium pool join --region auto \
    --sentinel us=us.sentinel.example.com:443 \
    --sentinel eu=eu.sentinel.example.com:443 \
    --pool prod --token-file /etc/containarium/join-token

BYO-compute (also register with a cloud control plane, using the same
token — the webui's "Add compute" one-liner sets this automatically):
  sudo containarium pool join \
    --sentinel asia-east1.containarium.dev:443 \
    --token-file /etc/containarium/join-token \
    --cloud-control-plane https://cloud.containarium.dev`,
	RunE: runPoolJoin,
}

func init() {
	poolCmd.AddCommand(poolJoinCmd)
	poolJoinCmd.Flags().StringArrayVar(&poolJoinSentinels, "sentinel", nil, "Sentinel this host dials, as host:port or region=host:port (repeatable). Pass several with --region auto to probe-and-select the closest (required)")
	poolJoinCmd.Flags().StringArrayVar(&poolJoinSentinelPins, "sentinel-pin", nil, "Public-key pin of a sentinel's tunnel identity, sha256:<hex> (repeatable, or comma-separated; or "+sentinelPinEnv+" env). With several --sentinel candidates, pass one pin per candidate; the tunnel accepts any pin in the list (required)")
	poolJoinCmd.Flags().StringVar(&poolJoinRegion, "region", "", "With multiple --sentinel candidates: 'auto' probes RTT and picks the closest, or a region name picks that one. Single --sentinel ignores this")
	poolJoinCmd.Flags().StringVar(&poolJoinToken, "token", "", "Scoped join token for the tunnel handshake. Lowest-precedence of the three token sources (see --token-file); lands on argv and in shell history, so prefer --token-file or $CONTAINARIUM_TUNNEL_TOKEN. Required if neither of those is set")
	poolJoinCmd.Flags().StringVar(&poolJoinTokenFile, "token-file", "", "File containing the scoped join token (read, trailing whitespace trimmed; error if empty). Takes precedence over $CONTAINARIUM_TUNNEL_TOKEN and --token — the recommended way to pass the token so it never lands on argv or in shell history")
	poolJoinCmd.Flags().StringVar(&poolJoinPool, "pool", "", "Pool to join (scopes daemon discovery + tunnel registration)")
	poolJoinCmd.Flags().StringVar(&poolJoinSpotID, "spot-id", "", "Unique id for this host in the pool (default: hostname)")
