package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	agentProvisionBoxBackendID string
	agentProvisionBoxPool      string
)

var agentProvisionBoxCmd = &cobra.Command{
	Use:   "provision-box <skill-id>",
	Short: "Provision a skill's box ahead of any run, with no model call",
	Long: `Creates or reuses the skill's deterministic box (agent-<skill-id>)
WITHOUT running it: no token is minted, nothing is seeded, and the model is
never called.

This exists so a crew member's box can be made to exist, and a human can
sign in to its coding agent, BEFORE any inference credential does.
'agent run'/crew runs are otherwise the only path that creates this box, and
both require a credential just to get that far — this breaks that
chicken-and-egg.

Typical flow:
  containarium agent provision-box engineer-crew-member --server <host>
  containarium connect agent-engineer-crew-member
  claude   # (or codex) — complete the device-code sign-in, once
  containarium agent credential-status engineer-crew-member --server <host>`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentProvisionBox,
}

