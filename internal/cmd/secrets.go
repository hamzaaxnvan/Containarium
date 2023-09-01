package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Tenant secrets — daemon-managed, AES-256-GCM in Postgres,
// stamped as environment.<NAME>=<value> on the LXC at container
// start. See docs/SECRETS-MANAGEMENT-DESIGN.md.
//
// Remote-only — there's no local fallback because the daemon owns
// the master key and the Postgres connection. Same pattern as
// `containarium monitoring`.

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage tenant secrets stored encrypted on the daemon",
	Long: `Manage tenant secrets (API keys, DB passwords, etc.) stored
encrypted at rest on the Containarium daemon. Values are stamped as
environment.<NAME>=<value> on the LXC at container start, so apps
inside docker read them via compose ${VAR} interpolation.

See docs/SECRETS-MANAGEMENT-DESIGN.md.`,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <username> <NAME> <value>",
	Short: "Create or update a tenant secret",
	Long: `Idempotent set-or-rotate. The first call creates the secret; later
calls with the same (username, NAME) bump the version and replace the
value. Names must match ^[A-Z_][A-Z0-9_]*$ (env-var convention);
values are capped at 64 KiB.

Containers stamp the new env on next CreateContainer / StartContainer.
For rotation against a running container, call:
  containarium secrets refresh <username>

Examples:
  containarium secrets set alice OPENAI_API_KEY sk-abc...
  containarium secrets set alice DATABASE_URL "postgres://..."`,
	Args: cobra.ExactArgs(3),
	RunE: runSecretsSet,
}

var secretsGetCmd = &cobra.Command{
	Use:   "get <username> <NAME>",
	Short: "Read a tenant secret's plaintext value",
	Long: `Returns the decrypted plaintext to stdout. Always audit-logged
on the daemon. Be mindful where you redirect the output.`,
	Args: cobra.ExactArgs(2),
	RunE: runSecretsGet,
}

var secretsListCmd = &cobra.Command{
	Use:   "list <username>",
	Short: "List a tenant's secrets (metadata only)",
	Long:  `Returns name/version/timestamps tuples. Values are only readable per-name via 'secrets get'.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runSecretsList,
}

var secretsDeleteCmd = &cobra.Command{
	Use:     "delete <username> <NAME>",
	Aliases: []string{"rm", "remove"},
	Short:   "Delete a tenant secret",
	Long: `Removes the secret from Postgres. Does NOT cascade to env-var
stamps on running containers — call 'secrets refresh' separately if
the change should reach the next exec without a container restart.`,
	Args: cobra.ExactArgs(2),
	RunE: runSecretsDelete,
}

var secretsRefreshCmd = &cobra.Command{
	Use:   "refresh <username>",
	Short: "Deliver the current secrets store to the tenant's box",
	Long: `Reads all of the tenant's secrets, decrypts them, and updates the
box to match.

On the LXC backend this sets the container's environment.<NAME>
config keys and rewrites tmpfs file-mode secrets. On the Kubernetes
backend it updates the box's mounted Secret, which the kubelet
refreshes in place within about a minute.

Either way, running processes keep their old environment (POSIX
inherit-at-fork); new execs and new sessions — including a fresh
'docker compose up' — see the refreshed values.

Use this after rotating a secret if you want the change to land
without restarting the box.`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsRefresh,
}

var secretsSetKMSKeyCmd = &cobra.Command{
	Use:   "set-kms-key <username> <key-resource-name>",
	Short: "Set a tenant's per-tenant KMS key for secrets (admin only)",
	Long: `Sets username's own GCP Cloud KMS CryptoKey as the key their secrets
are wrapped under going forward, and re-wraps every secret they
currently own under it (#1630).

Requires the daemon's CONTAINARIUM_KMS_BACKEND=gcp, and a token with
the admin role AND the secrets:write scope granted EXPLICITLY — the
admin role alone is not enough for this RPC, unlike the other
'secrets' subcommands. Mint one with:
  containarium token generate --roles admin --scopes secrets:write ...

key-resource-name is a full GCP Cloud KMS CryptoKey resource path:
  projects/<project>/locations/<location>/keyRings/<ring>/cryptoKeys/<key>

Use 'secrets clear-kms-key' to revert username to the shared KEK.`,
	Args: cobra.ExactArgs(2),
	RunE: runSecretsSetKMSKey,
}

var secretsClearKMSKeyCmd = &cobra.Command{
	Use:   "clear-kms-key <username>",
	Short: "Stop using a tenant's per-tenant KMS key (admin only)",
	Long: `Stops routing username's NEW secrets to their per-tenant KMS key —
future writes fall back to the daemon's shared KEK. This does NOT
touch any secret username already owns: each existing row stays
wrapped under whatever key it was already wrapped under. If that key
is later destroyed (the cloud control plane does this right after
calling this command, as the cryptographic-shred half of per-org CMEK
disable), those pre-existing secrets become permanently unreadable —
this is not a reversible "revert," it's the last step before a shred.
Idempotent — clearing a tenant with no override still succeeds.

Same authz requirement as 'secrets set-kms-key': admin role + the
secrets:write scope granted explicitly on the token.`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsClearKMSKey,
}

// secretsDelivery is the value bound to `--delivery` on
// `secrets set` (Phase 4.3 Phase A). Allowed values: "",
// "env" (default; server normalizes ""→"env"), "file"
// (planned tmpfs delivery, Phase B will wire behavior).
var secretsDelivery string

