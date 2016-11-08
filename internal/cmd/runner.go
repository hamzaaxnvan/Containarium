package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/runner"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
	"github.com/spf13/cobra"
)

// Flags for `containarium runner provision`. Kept as package-level
// vars to match the pattern in create.go / delete.go / list.go.
var (
	runnerPAT          string
	runnerCount        int
	runnerNamePrefix   string
	runnerLabels       string
	runnerGroup        string
	runnerNameTemplate string
	runnerSSHKeyPath   string
	runnerSentinelHost string
	runnerSSHUser      string
	runnerBackendID    string
	runnerPool         string

	// runner list / remove
	runnerListFormat string
)

var runnerCmd = &cobra.Command{
	Use:   "runner",
	Short: "Manage Containarium boxes as GitHub Actions self-hosted runners",
	Long: `Provision, list, and remove Containarium boxes configured as
ephemeral GitHub Actions self-hosted runners.

This is the agent-friendly counterpart to the manual flow documented in
hacks/runner/README.md. The CLI verbs here drive the same install script
(embedded at compile time), so an agent calling ` + "`provision_runners`" + `
via MCP and a human running ` + "`containarium runner provision`" + ` both
end up with provably identical runners.

See ` + "`containarium runner provision --help`" + ` for the most common
entry point.`,
}

var runnerProvisionCmd = &cobra.Command{
	Use:   "provision <target>",
	Short: "Create N runner boxes and register them as ephemeral GHA runners",
	Long: `Create N Containarium boxes and configure each as an ephemeral
GitHub Actions self-hosted runner for the given target.

<target> is either "owner/repo" for a single repository, or a bare "owner"
for an ORGANIZATION runner that every repo in the org can use (#1217). The
shape selects the scope, the way GitHub itself disambiguates. An org target
needs a token with the admin:org scope rather than repo.

This verb is idempotent: re-running with the same args after a partial
failure is safe. Boxes that already exist are not recreated; boxes that
already have containarium-runner.service installed and enabled are not
re-installed. Agents can call this repeatedly to reconcile state.

Examples:
  # Provision 3 ephemeral runners for footprintai/containarium
  containarium runner provision footprintai/containarium \
      --github-pat ghp_xxxx --count 3

  # Override the prefix (boxes named ci-myrepo-1, -2, -3)
  containarium runner provision footprintai/containarium \
      --github-pat ghp_xxxx --count 3 --name-prefix ci-myrepo

  # Custom labels (workflows target with runs-on: [self-hosted, gpu])
  containarium runner provision footprintai/containarium \
      --github-pat ghp_xxxx --count 2 --labels gpu,containarium

  # Organization runners in the org's "gpu" runner group
  containarium runner provision footprintai \
      --github-pat ghp_xxxx --count 2 --runner-group gpu`,
	Args: cobra.ExactArgs(1),
	RunE: runRunnerProvision,
}

var runnerListCmd = &cobra.Command{
	Use:   "list <target>",
	Short: "List provisioned runner boxes and their GitHub registration status",
	Long: `List Containarium boxes whose name starts with --name-prefix and
merge their GitHub-side registration status (online / offline / busy /
unregistered) into a single view.

Read-only.`,
	Args: cobra.ExactArgs(1),
	RunE: runRunnerList,
}

var runnerRemoveCmd = &cobra.Command{
	Use:   "remove <target> <name>",
	Short: "Drain a runner, deregister it from GitHub, and delete the box",
	Long: `Stop the runner service inside the box (which waits for the in-flight
ephemeral job to finish — this is what "drain" means in the ephemeral
model), deregister the runner from GitHub, then delete the Containarium
box.

GitHub-side deregister is best-effort: if the API call fails, we still
proceed to delete the box so a transient GitHub blip doesn't leak a
box. A stale "offline" row may remain in GitHub's UI; remove it manually
or re-run this command once the API is back.`,
	Args: cobra.ExactArgs(2),
	RunE: runRunnerRemove,
}

func init() {
	rootCmd.AddCommand(runnerCmd)
	runnerCmd.AddCommand(runnerProvisionCmd)
	runnerCmd.AddCommand(runnerListCmd)
	runnerCmd.AddCommand(runnerRemoveCmd)

	// Provision flags.
	runnerProvisionCmd.Flags().StringVar(&runnerPAT, "github-pat", os.Getenv("GH_PAT"), "GitHub PAT: `repo` scope for an owner/repo target, `admin:org` for an organization (env: GH_PAT). REQUIRED.")
	runnerProvisionCmd.Flags().IntVar(&runnerCount, "count", 1, "How many runners to provision (1..100)")
	runnerProvisionCmd.Flags().StringVar(&runnerNamePrefix, "name-prefix", "ci-runner", "Prefix for generated box names")
	runnerProvisionCmd.Flags().StringVar(&runnerLabels, "labels", "containarium,ephemeral", "Comma-separated runner labels")
	runnerProvisionCmd.Flags().StringVar(&runnerGroup, "runner-group", "", "Organization runner group to register into (organization targets only; default: the org's default group)")
	runnerProvisionCmd.Flags().StringVar(&runnerNameTemplate, "runner-name-template", "{prefix}-{i}", "Template for box names; {prefix} and {i} are substituted")
	runnerProvisionCmd.Flags().StringVar(&runnerSSHKeyPath, "ssh-key", "", "Path to SSH public key used when creating new boxes (default: ~/.ssh/id_rsa.pub)")
	runnerProvisionCmd.Flags().StringVar(&runnerSentinelHost, "sentinel", os.Getenv(config.EnvSentinelHost), "Sentinel SSH host (env: CONTAINARIUM_SENTINEL_HOST). REQUIRED for the install step.")
	runnerProvisionCmd.Flags().StringVar(&runnerSSHUser, "ssh-user", "", "SSH user to use when SSH'ing into a runner box (default: the runner name, via sshpiper)")
	runnerProvisionCmd.Flags().StringVar(&runnerBackendID, "backend-id", "", "Place the runner boxes on a specific backend (e.g. a named BYOC host). Same semantics as `containarium create --backend-id`; empty keeps today's default placement.")
	runnerProvisionCmd.Flags().StringVar(&runnerPool, "pool", "", "Place the runner boxes on any healthy backend in this pool. Same semantics as `containarium create --pool`; mutually exclusive with --backend-id, validated by the daemon.")

	// List flags.
	runnerListCmd.Flags().StringVar(&runnerPAT, "github-pat", os.Getenv("GH_PAT"), "GitHub PAT: `repo` scope for an owner/repo target, `admin:org` for an organization (env: GH_PAT). REQUIRED.")
	runnerListCmd.Flags().StringVar(&runnerNamePrefix, "name-prefix", "ci-runner", "Box name prefix to filter on")
	runnerListCmd.Flags().StringVar(&runnerListFormat, "format", "table", "Output format: table or json")

	// Remove flags. Reuse PAT/sentinel from above.
	runnerRemoveCmd.Flags().StringVar(&runnerPAT, "github-pat", os.Getenv("GH_PAT"), "GitHub PAT: `repo` scope for an owner/repo target, `admin:org` for an organization (env: GH_PAT). REQUIRED.")
}

// runRunnerProvision is the cobra handler. Most of the actual
// work lives in runner.Provision; this glue layer's job is to
// turn cobra flags into a runner.Options + Deps and render the
// resulting Result for the human.
func runRunnerProvision(_ *cobra.Command, args []string) error {
	repo := args[0]
	opts := runner.Options{
		Repo:         repo,
		PAT:          runnerPAT,
		Count:        runnerCount,
		NamePrefix:   runnerNamePrefix,
		Labels:       runnerLabels,
		RunnerGroup:  runnerGroup,
		NameTemplate: runnerNameTemplate,
	}
	if err := runner.ValidateOptions(opts); err != nil {
		return err
	}
	if runnerSentinelHost == "" {
		return fmt.Errorf("--sentinel is required (or set CONTAINARIUM_SENTINEL_HOST); the install step needs to SSH into each new box")
	}

	deps, sshKey, err := buildRunnerDeps(runnerSentinelHost, runnerSSHUser)
	if err != nil {
		return err
	}
	// Reuse the operator's existing SSH public key when creating
	// the boxes so the same private key auths the install step.
	opts.SSHKey = sshKey
