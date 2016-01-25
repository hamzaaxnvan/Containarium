// `containarium security bad-destinations` — list, add, and remove entries
// in the known-bad-destination list the bad-destination rule (#1641)
// matches flow destinations against. CLI-first per the repo convention; the
// MCP tools (internal/mcp/security.go) wrap the same REST calls.
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var securityBadDestinationsCmd = &cobra.Command{
	Use:   "bad-destinations",
	Short: "Manage the known-bad-destination list (bad-destination rule, #1641)",
}

var badDestinationsListJSONOut bool

var securityBadDestinationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the merged baseline + operator-added known-bad destinations",
	Args:  cobra.NoArgs,
	RunE:  runSecurityBadDestinationsList,
}

var securityBadDestinationsAddCmd = &cobra.Command{
	Use:   "add <cidr> [label]",
	Short: "Add an operator-supplied entry to the known-bad-destination list",
	Long: `Adds an exact IP or CIDR to the known-bad-destination list, effective
immediately — no daemon rebuild or restart required.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSecurityBadDestinationsAdd,
}

var securityBadDestinationsRemoveCmd = &cobra.Command{
	Use:   "remove <cidr>",
	Short: "Remove a previously operator-added entry",
	Long:  `Removes a previously operator-added entry. Baseline entries cannot be removed.`,
