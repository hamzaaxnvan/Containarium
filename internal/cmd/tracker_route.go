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
}

var (
	trackerRouteScope       string
	trackerRouteSkill       string
	trackerRouteDeleteScope string
)

func init() {
	trackerCmd.AddCommand(trackerRouteCmd)

	trackerRouteCmd.AddCommand(trackerRouteSetCmd)
	trackerRouteSetCmd.Flags().StringVar(&trackerRouteScope, "scope", "",
		`Label suffix: "product" matches the label "scope:product". Required.`)
	trackerRouteSetCmd.Flags().StringVar(&trackerRouteSkill, "skill", "",
		"Agent skill id to run for issues with this scope label (see `containarium agent list`). Required.")

	trackerRouteCmd.AddCommand(trackerRouteListCmd)

	trackerRouteCmd.AddCommand(trackerRouteDeleteCmd)
	trackerRouteDeleteCmd.Flags().StringVar(&trackerRouteDeleteScope, "scope", "", "Label suffix of the route to remove. Required.")
}

// trackerRouteClient is the slice of the typed client the route
// subcommands need; *client.GRPCClient and *client.HTTPClient both
// satisfy it, so each handler is written once for both transports.
type trackerRouteClient interface {
	SetTrackerRoute(req *pb.SetTrackerRouteRequest) (*pb.TrackerRoute, string, error)
	ListTrackerRoutes(username, connection string) ([]*pb.TrackerRoute, error)
	DeleteTrackerRoute(req *pb.DeleteTrackerRouteRequest) (string, error)
	Close() error
}

var (
	_ trackerRouteClient = (*client.GRPCClient)(nil)
	_ trackerRouteClient = (*client.HTTPClient)(nil)
)

func newTrackerRouteClient() (trackerRouteClient, error) {
	if serverAddr == "" {
		return nil, fmt.Errorf("--server is required for tracker commands (daemon owns the route store)")
	}
	if httpMode {
		return client.NewHTTPClient(serverAddr, authToken)
	}
	return client.NewGRPCClient(serverAddr, certsDir, insecure)
}

// scopeLabelPrefix mirrors internal/tracker.ScopeLabelPrefix. Not
// imported: internal/tracker links pgx, which the client build must not
