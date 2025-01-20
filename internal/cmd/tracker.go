package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Tracker connections — where a tenant's issue tracker is, and which
// broker-only secret holds the credential the daemon uses to reach it
// (#1921). The credential never enters a box; only its name (a secret
// already set with `secrets set --delivery broker`) is accepted
// here. See docs/architecture/agent-tracker-broker.md.
//
// Remote-only, same reasoning as `secrets`: the daemon owns the
// Postgres connection this is stored in.
//
// Connection CRUD requires tracker:admin on the token — separate from
// the tracker:read / tracker:write scopes an agent's run gets for the
// tracker verbs themselves (#1922), which cannot repoint a connection.

var trackerCmd = &cobra.Command{
	Use:   "tracker",
	Short: "Manage tracker connections (GitHub/GitLab, broker-only credential)",
	Long: `Register where a tenant's issue tracker is and which broker-only
secret holds its credential, so agent runs can be granted tracker
access (#1922) without any box ever holding the credential.

See docs/architecture/agent-tracker-broker.md.`,
}

var trackerConnectCmd = &cobra.Command{
	Use:   "connect <username> <name>",
	Short: "Create or update a tracker connection",
	Long: `Idempotent — repeated calls with the same (username, name) replace
the connection's fields. --credential-secret must already name a
secret owned by username in broker-only delivery mode:

  containarium secrets set alice GH_TOKEN ghp_... --delivery broker
  containarium tracker connect alice default \
    --provider github --project acme/widgets --credential-secret GH_TOKEN

Referencing a secret in any other delivery mode is rejected — a
tracker connection must never point at a secret a box can also read.`,
	Args: cobra.ExactArgs(2),
	RunE: runTrackerConnect,
}

var trackerListCmd = &cobra.Command{
	Use:   "list <username>",
	Short: "List a tenant's tracker connections",
	Args:  cobra.ExactArgs(1),
	RunE:  runTrackerList,
}

var trackerDisconnectCmd = &cobra.Command{
	Use:     "disconnect <username> <name>",
	Aliases: []string{"rm", "remove", "delete"},
	Short:   "Remove a tracker connection",
	Long:    `Removes the named connection. Does NOT delete the credential secret it referenced.`,
	Args:    cobra.ExactArgs(2),
	RunE:    runTrackerDisconnect,
}

var trackerStatusCmd = &cobra.Command{
	Use:   "status <username> <name>",
	Short: "Check a tracker connection's live status",
	Long: `Asks the tracker to describe the connection's own credential:
reachability, whether the credential is still valid, its granted
scopes and expiry, and whether it's broader than the provider's
preferred credential type (a GitLab project access token; a GitHub
App installation token or single-repository fine-grained PAT).

A "broad" warning is not an error — the connection still works. It
means a daemon compromise could do more with this credential than the
narrowest type would have allowed.`,
	Args: cobra.ExactArgs(2),
