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
