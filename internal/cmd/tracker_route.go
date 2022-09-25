package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Scope routes (#2021): map a `scope:<role>` label on a tracker
// connection to the skill the dispatcher (#2022) starts for it. An
// unmapped scope label is never dispatched. Requires tracker:admin, like
// connection CRUD. See docs/architecture/issue-triggered-agents.md.

var trackerRouteCmd = &cobra.Command{
	Use:   "route",
	Short: "Map scope:<role> labels to skills on a tracker connection",
	Long: `A route sends issues labeled scope:<scope> on one tracker connection to
one agent skill. --scope is the label suffix: --scope product matches the
label "scope:product".

  containarium tracker route set alice default --scope product --skill product-define
  containarium tracker route list alice default
  containarium tracker route delete alice default --scope product

Deleting the connection deletes its routes.`,
}

var trackerRouteSetCmd = &cobra.Command{
	Use:   "set <username> <connection> --scope <scope> --skill <skill-id>",
	Short: "Create or update the route for one scope label",
	Args:  cobra.ExactArgs(2),
	RunE:  runTrackerRouteSet,
}

var trackerRouteListCmd = &cobra.Command{
	Use:   "list <username> <connection>",
	Short: "List a connection's scope routes",
	Args:  cobra.ExactArgs(2),
	RunE:  runTrackerRouteList,
}

var trackerRouteDeleteCmd = &cobra.Command{
	Use:     "delete <username> <connection> --scope <scope>",
	Aliases: []string{"rm", "remove"},
	Short:   "Remove the route for one scope label",
	Args:    cobra.ExactArgs(2),
	RunE:    runTrackerRouteDelete,
