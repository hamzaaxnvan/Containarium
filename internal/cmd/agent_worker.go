package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// agent_worker.go — start the consumer side of the pull-based run queue
// (prototype). The daemon provisions the skill's box, mints a queue credential
// scoped to agents:run, and launches the in-box runtime in poll mode; the
// worker then leases and runs tasks enqueued with `agent enqueue`.
// See docs/AGENT-MODEL-GATEWAY-DESIGN.md (pull-queue section).

var (
	agentWorkerBackendID string
	agentWorkerPool      string
	agentWorkerID        string
)

var agentWorkerCmd = &cobra.Command{
	Use:   "worker <skill-id>",
	Short: "Start a pull-queue worker box for a skill (prototype)",
	Long: `Provision the skill's box, mint a queue credential (scoped to agents:run,
separate from the skill's in-box token), and launch the in-box runtime in poll
mode. The worker leases tasks for the skill, runs them locally, and reports the
results back — all outbound from the box.

Pair with the producer: enqueue work with 'containarium agent enqueue'.

Examples:
  containarium agent worker hello-agent --server <host>
  containarium agent enqueue hello-agent --input '{"q":"hi"}' --server <host>`,
	Args: cobra.ExactArgs(1),
