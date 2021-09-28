package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/gatewayprovider"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
)

// `containarium gateway` — the model gateway's admin + mint surface (#1726).
//
// CLI-first, per the repo's own rule: every verb here is a thin wrapper over the
// same ModelGatewayService RPC the MCP tool calls, so a human can drive the whole
// feature from a shell and the agent surface adds no capability of its own.
//
// Remote-only. The daemon holds the real provider keys and signs the tokens;
// there is nothing a local fallback could do.

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Model-gateway provider keys and scoped box tokens",
	Long: `Manage the daemon's model gateway: which upstream key each key owner
pays with, and the short-lived scoped tokens a box presents instead of one.

A box never holds a real provider key. It holds a gateway token bound to the
box, the provider, the resolved key owner, optionally one run and one model
ceiling — revocable by its jti and expiring on its own.

Key owners are namespaced: user:<username> for a self-hosted box, org:<org_id>
for a cloud-attributed one.`,
}

var gatewayKeyCmd = &cobra.Command{
	Use:   "key",
	Short: "Register, inspect or remove a key owner's upstream provider key",
	Long: `A key owner's real upstream API key, held encrypted on the daemon and
never delivered to any box.

Write-only: there is no verb, here or on the API, that reads a registered key
back. "status" reports a fingerprint and when it was set.

Requires the gateway:admin scope.`,
}

var (
	gatewayProviderFlag string
	gatewayKeyValue     string
	gatewayKeyFile      string
	gatewayMintRunID    string
	gatewayMintTTL      string
	gatewayMintModels   []string
	gatewayMintDryRun   bool
	gatewayMintEnv      bool
	gatewayModelsBox    string
)

var gatewayKeySetCmd = &cobra.Command{
	Use:   "set <key-owner>",
	Short: "Store or rotate a key owner's upstream provider key",
	Long: `Idempotent set-or-rotate for one (key owner, provider) pair.

Prefer --key-file over --key: a key passed as an argument lands in your shell
history and in the process table.

Examples:
  containarium gateway key set user:alice --provider kafeido --key-file ./key.txt
  containarium gateway key set org:0b1c… --provider kafeido --key sk-…`,
	Args: cobra.ExactArgs(1),
	RunE: runGatewayKeySet,
}

var gatewayKeyDeleteCmd = &cobra.Command{
	Use:     "delete <key-owner>",
	Aliases: []string{"rm", "remove"},
	Short:   "Remove a key owner's provider key and revoke its live tokens",
	Long: `Removes the stored key AND revokes every gateway token already issued
for that owner, so the key stops being spendable on the next call rather than
when the last token expires.

If the daemon holds no revocable owner-revocation store, the command says so:
