package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/spf13/cobra"
)

var containerGetFormat string

var getCmd = &cobra.Command{
	Use:   "get <username>",
	Short: "Get one container's current state",
	Long: `Look up a single container by username — the O(1) counterpart to
'containarium list', which always fetches and filters every container on
the backend even when only one is wanted.

Found live (#1541): a script polling readiness with 'list' in a tight loop
makes the backend do 2 Incus API round-trips per EXISTING container on
every single poll, regardless of how many containers the caller actually
cares about — at a few hundred containers this becomes real load on the
Incus daemon itself. 'get' costs exactly one round-trip, for the one
container asked about, no matter how many others exist. Prefer it over
'list --user <name>' for polling loops and any other single-container
lookup.

Examples:
  containarium get alice
  containarium get alice --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runGet,
}

func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.Flags().StringVarP(&containerGetFormat, "format", "f", "table", "Output format: table, json, yaml")
}

func runGet(cmd *cobra.Command, args []string) error {
	username := args[0]
