package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	agentCallFrom  string
	agentCallInput string
)

var agentCallCmd = &cobra.Command{
	Use:   "call <peer-skill-id>",
	Short: "Delegate a task to a running peer agent (A2A)",
	Long: `Send a task to a running peer agent over the agent-to-agent transport and
print its artifact. The peer must already be running (containarium agent run
<peer>).

Phase 1: the peer's in-box A2A server (the agent-runtime image's job) receives
the task. Until that ships, a call reaches no listener and returns an error.

Examples:
  containarium agent call hello-agent --from my-agent --input '{"q":"hi"}' --server <host>`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentCall,
}

func init() {
	agentCmd.AddCommand(agentCallCmd)
	agentCallCmd.Flags().StringVar(&agentCallFrom, "from", "",
		"Calling skill id (for attribution + the future allowed_peers check)")
	agentCallCmd.Flags().StringVar(&agentCallInput, "input", "",
		"Task input as a JSON string (defaults to {})")
}
