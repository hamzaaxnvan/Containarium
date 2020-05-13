package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/client"
)

// `containarium sessions` (#2522) controls live SSH sessions on a backend.
var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Control live SSH sessions",
}

var sessionsTerminateUser string

var sessionsTerminateCmd = &cobra.Command{
	Use:   "terminate --user <name>",
	Short: "End every live SSH session a user holds on the backend",
	Long: `End every live SSH session a user holds on the backend: their own box and
every box they reach as a collaborator, inside the box and on the host jump
account.

Nothing is deleted and no authorized_keys file changes: the user can connect
again. Requires ssh:write; an admin may name any user, anyone else only
themselves. Requires --server.

Examples:
  containarium sessions terminate --user alice --server daemon.example.com:50051`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		user := strings.TrimSpace(sessionsTerminateUser)
