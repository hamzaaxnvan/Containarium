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
