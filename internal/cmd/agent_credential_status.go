package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/agentengine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var agentCredentialStatusCmd = &cobra.Command{
	Use:   "credential-status <skill-id>",
	Short: "Check whether a skill's box already has a coding-agent credential",
	Long: `Reports the NAME of the credential source (interactive sign-in, a
user-placed key, or none) the skill's box would use for its configured
engine — NEVER the credential's value. Mirrors 'containarium code install'
own verify output: a presence check only, never a read of the credential
itself.

The box must already be provisioned (see 'agent provision-box').`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentCredentialStatus,
}

func init() {
	agentCmd.AddCommand(agentCredentialStatusCmd)
}

func runAgentCredentialStatus(cmd *cobra.Command, args []string) error {
	skillID := args[0]

	c, err := newAgentClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.GetSkillBoxCredentialStatus(skillID)
