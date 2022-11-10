package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var agentGetCmd = &cobra.Command{
	Use:   "get <skill-id>",
	Short: "Show an agent skill's definition",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentGet,
}

func init() {
	agentCmd.AddCommand(agentGetCmd)
}

func runAgentGet(cmd *cobra.Command, args []string) error {
	id := args[0]

