package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/footprintai/containarium/internal/agentengine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	agentRunBackendID         string
	agentRunPool              string
	agentRunInput             string
	agentRunGitSource         string
	agentRunGitRef            string
	agentRunGitCredentialFile string
	agentRunTrackerConnection string
	agentRunEngine            string
)

var agentRunCmd = &cobra.Command{
	Use:   "run <skill-id>",
	Short: "Run an agent skill in a box",
	Long: `Provision the skill's box, mint a token scoped to exactly the skill's
allowed_scopes, seed the system prompt + token + task input into the box, and
return the box.

Phase 0: the in-box agent loop (the agent-runtime image's job) consumes the
seed; the returned artifact is empty until that lands.

Examples:
  containarium agent run hello-agent --input '{"q":"hi"}' --server <host>
  containarium agent run code-review --git-source https://github.com/org/repo \
    --git-ref main --server <host>
  containarium agent run triage --tracker-connection <conn> --server <host>
  containarium agent run hello-agent --engine codex --server <host>`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentRun,
}

func init() {
	agentCmd.AddCommand(agentRunCmd)
	agentRunCmd.Flags().StringVar(&agentRunBackendID, "backend-id", "",
		"Target backend ID (must be the local backend in v1)")
	agentRunCmd.Flags().StringVar(&agentRunPool, "pool", "",
		"Target pool (not supported in v1)")
	agentRunCmd.Flags().StringVar(&agentRunInput, "input", "",
		"Task input as a JSON string (defaults to {})")
	agentRunCmd.Flags().StringVar(&agentRunGitSource, "git-source", "",
		"Git clone URL fetched into the run's workspace before the agent starts (e.g. https://github.com/org/repo). Empty = no fetch.")
	agentRunCmd.Flags().StringVar(&agentRunGitRef, "git-ref", "",
		"Exact ref to check out for --git-source: full SHA (preferred), branch, tag, or refs/pull/N/merge. Empty = the remote's default branch.")
	agentRunCmd.Flags().StringVar(&agentRunGitCredentialFile, "git-credential-file", "",
		"Path to a file holding a bearer token for a private --git-source. Used daemon-side for one fetch; never written to the box's .git/config.")
	agentRunCmd.Flags().StringVar(&agentRunTrackerConnection, "tracker-connection", "",
		"Name of one of your tracker connections to bind this run to; minted into the run token as its tracker_conn claim. The daemon rejects a name you don't own. Empty = no binding.")
	agentRunCmd.Flags().StringVar(&agentRunEngine, "engine", "",
		"Override the skill manifest's own engine for this run: one of "+strings.Join(agentengine.Names(), ", ")+". Refused with the same error a manifest-named engine gets when its provider has no key. Empty = use the manifest.")
}

// resolveAgentRunEngine parses --engine, empty meaning "no override" (the
// manifest decides) — agentengine.Parse itself treats "" as an error, since
// an explicit name is what it exists to validate, not the absence of one.
func resolveAgentRunEngine() (pb.AgentEngine, error) {
	if agentRunEngine == "" {
		return pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED, nil
	}
	return agentengine.Parse(agentRunEngine)
}
