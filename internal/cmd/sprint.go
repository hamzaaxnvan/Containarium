package cmd

import (
	"errors"
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

// `containarium sprint list|board` reads the sprint board a hosted control
// plane mirrors onto each sprint umbrella issue. The platform MCP tools
// sprint_list and sprint_board call the same internal/client functions and
// formatters, so the CLI and an agent see identical results.
//
// REST only: the routes (GET /v1/orgs/{org}/sprints[/board]) exist only on a
// hosted control plane and have no gRPC transport here. Read-only.

var (
	sprintOrg      string
	sprintProject  string
	sprintNumber   int64
	sprintProvider string
)

var sprintCmd = &cobra.Command{
	Use:   "sprint",
	Short: "Read sprint boards mirrored by a hosted control plane",
	Long: `Read the sprint board a hosted control plane mirrors onto each sprint
umbrella issue. Read-only: boards are read, never computed live.

These commands need a hosted control plane. A standalone daemon does not
serve the sprint routes and the command says so.`,
}

var sprintListCmd = &cobra.Command{
	Use:   "list --org <org>",
	Short: "List an organization's sprints with each one's mirror state",
	Long: `List the organization's sprints (open issues labelled "sprint" on
repositories that opted in) with each one's mirror state. Use "sprint board"
to read a board.`,
	Args: cobra.NoArgs,
