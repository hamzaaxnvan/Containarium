package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// agent_enqueue.go — producer side of the pull-based run queue (prototype).
// Where `agent run` provisions a box and runs synchronously (push), `agent
// enqueue` drops a task on the queue for a long-lived worker box to lease and
// run (pull). See docs/AGENT-MODEL-GATEWAY-DESIGN.md (pull-queue section).

var agentEnqueueInput string

var agentEnqueueCmd = &cobra.Command{
	Use:   "enqueue <skill-id>",
	Short: "Enqueue a task on the pull-based agent run queue (prototype)",
	Long: `Place a task on the queue for a skill. Worker boxes running in poll mode
lease the task, run it locally, and report the result back — no inbound exec to
the box is needed (NAT/tunnel-friendly).

Examples:
  containarium agent enqueue hello-agent --input '{"q":"hi"}' --server <host>`,
	Args: cobra.ExactArgs(1),
