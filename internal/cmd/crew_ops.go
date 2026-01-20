package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/pkg/core/crews"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	crewRunGraphFile         string
	crewRunConcurrency       uint32
	crewRunBackendID         string
	crewRunPool              string
	crewRunInput             string
	crewRunGitSource         string
	crewRunGitRef            string
	crewRunGitCredentialFile string
	crewRunEngineOverrides   []string
	crewRunModelOverrides    []string
)

var crewListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List available crews",
	Args:    cobra.NoArgs,
	RunE:    runCrewList,
}

var crewGetCmd = &cobra.Command{
	Use:   "get <crew-id>",
	Short: "Show a crew's definition",
	Args:  cobra.ExactArgs(1),
	RunE:  runCrewGet,
}

var crewRunCmd = &cobra.Command{
	Use:   "run <crew-id>",
	Short: "Run a crew",
	Long: `Provision every member's box (scoped token + per-box allowed_peers policy),
start them in serve mode, and drive the crew's topology over A2A.

--git-source/--git-ref (#1554) fetch the SAME repo+ref into EVERY member's
own per-run workspace before the crew is driven — the shared codebase
between crew members is git at a pinned SHA, not a shared filesystem.

Examples:
  containarium crew run hello-crew --input '{"q":"hi"}' --server <host>
  containarium crew run freeform-crew --git-source https://github.com/org/repo     --git-ref main --server <host>
  containarium crew run two-engine-crew --engine hello-agent-claude=claude --engine hello-agent-codex=codex --server <host>`,
	Args: cobra.ExactArgs(1),
	RunE: runCrewRun,
}

var crewStatusCmd = &cobra.Command{
	Use:   "status <run-id>",
	Short: "Show a crew run's status",
	Args:  cobra.ExactArgs(1),
	RunE:  runCrewStatus,
}

func init() {
	crewCmd.AddCommand(crewListCmd, crewGetCmd, crewRunCmd, crewStatusCmd)
	crewRunCmd.Flags().StringVar(&crewRunGraphFile, "plan", "", "TaskGraph YAML/JSON file (ORCHESTRATOR/FREEFORM only)")
	crewRunCmd.Flags().Uint32Var(&crewRunConcurrency, "concurrency", 0, "Parallel graph nodes (0 defaults to 5)")
	crewRunCmd.Flags().StringVar(&crewRunBackendID, "backend-id", "", "Target backend ID")
	crewRunCmd.Flags().StringVar(&crewRunPool, "pool", "", "Target pool")
	crewRunCmd.Flags().StringVar(&crewRunInput, "input", "", "Crew input as a JSON string (defaults to {})")
	crewRunCmd.Flags().StringVar(&crewRunGitSource, "git-source", "",
		"Git clone URL fetched into EVERY member's own per-run workspace before the crew is driven (e.g. https://github.com/org/repo). Empty = no fetch.")
	crewRunCmd.Flags().StringVar(&crewRunGitRef, "git-ref", "",
		"Exact ref to check out for --git-source: full SHA (preferred), branch, tag, or refs/pull/N/merge. Empty = the remote's default branch.")
	crewRunCmd.Flags().StringVar(&crewRunGitCredentialFile, "git-credential-file", "",
		"Path to a file holding a bearer token for a private --git-source. Used daemon-side for each member's fetch; never written to any box's .git/config.")
	crewRunCmd.Flags().StringArrayVar(&crewRunEngineOverrides, "engine", nil,
		"Override one member's engine: <skill-id>=<engine>, one of "+strings.Join(agentengine.Names(), ", ")+" (repeatable). A member with no entry keeps its manifest's own choice.")
	crewRunCmd.Flags().StringArrayVar(&crewRunModelOverrides, "model", nil,
		"Override one member's model: <skill-id>=<model-id> (repeatable). The id is passed through as is; a member with no entry keeps its skill's own model. Needs a named engine for that member (its manifest's, or --engine).")
}

// resolveCrewRunModelOverrides parses --model <skill-id>=<model-id> pairs into
// the per-member map RunCrewRequest.model_overrides carries (#3259). A member
// named more than once is an error, for the same reason as --engine.
func resolveCrewRunModelOverrides() (map[string]string, error) {
	if len(crewRunModelOverrides) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(crewRunModelOverrides))
	for _, kv := range crewRunModelOverrides {
		skillID, model, ok := strings.Cut(kv, "=")
		if !ok || skillID == "" || model == "" {
			return nil, fmt.Errorf("invalid --model %q (want <skill-id>=<model-id>)", kv)
		}
		if _, exists := out[skillID]; exists {
			return nil, fmt.Errorf("--model %s given more than once", skillID)
		}
		out[skillID] = model
	}
	return out, nil
}

// resolveCrewRunEngineOverrides parses --engine <skill-id>=<engine> pairs
// into the per-member map RunCrewRequest.engine_overrides carries. A member
// named more than once is an error — a silent "last one wins" would let a
// typo in one flag quietly undo another.
func resolveCrewRunEngineOverrides() (map[string]pb.AgentEngine, error) {
	if len(crewRunEngineOverrides) == 0 {
		return nil, nil
	}
	out := make(map[string]pb.AgentEngine, len(crewRunEngineOverrides))
	for _, kv := range crewRunEngineOverrides {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid --engine %q (want <skill-id>=<engine>)", kv)
		}
		skillID, engineName := parts[0], parts[1]
		if _, exists := out[skillID]; exists {
			return nil, fmt.Errorf("--engine %s given more than once", skillID)
		}
		engine, err := agentengine.Parse(engineName)
		if err != nil {
			return nil, fmt.Errorf("--engine %s: %w", skillID, err)
		}
		out[skillID] = engine
	}
	return out, nil
}

// resolveCrewRunGitCredential reads --git-credential-file if one was
// supplied, mirroring resolveAgentRunGitCredential's handling of the same
// flag on `containarium agent run`.
func resolveCrewRunGitCredential() (string, error) {
	if crewRunGitCredentialFile == "" {
		return "", nil
	}
	// #nosec G304 -- operator-supplied path; reading it is the documented
	// purpose of --git-credential-file (same trust as reading an SSH key file).
	data, err := os.ReadFile(crewRunGitCredentialFile)
	if err != nil {
		return "", fmt.Errorf("failed to read --git-credential-file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func runCrewList(cmd *cobra.Command, args []string) error {
