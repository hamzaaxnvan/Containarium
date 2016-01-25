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
	Args:  cobra.ExactArgs(1),
	RunE:  runSecurityBadDestinationsRemove,
}

// badDestinationEnvelope mirrors *BadDestinationEntry's grpc-gateway JSON
// shape (protojson default: camelCase field names are already snake_case
// here since every field name is a single word).
type badDestinationEnvelope struct {
	CIDR   string `json:"cidr"`
	Label  string `json:"label"`
	Source string `json:"source"`
}

type listBadDestinationsEnvelope struct {
	Entries []badDestinationEnvelope `json:"entries"`
}

type addBadDestinationEnvelope struct {
	Entry badDestinationEnvelope `json:"entry"`
}

func runSecurityBadDestinationsList(cmd *cobra.Command, _ []string) error {
	if serverAddr == "" {
		return errServerRequired()
	}
	var out listBadDestinationsEnvelope
	url := strings.TrimSuffix(serverAddr, "/") + "/v1/security/bad-destinations"
	if err := getJSON(url, &out); err != nil {
		return err
	}
	if badDestinationsListJSONOut {
		return printJSON(out)
	}
	w := cmd.OutOrStdout()
	if len(out.Entries) == 0 {
		fmt.Fprintln(w, "No known-bad destinations configured.")
		return nil
	}
	fmt.Fprintf(w, "%-24s %-10s %s\n", "CIDR", "SOURCE", "LABEL")
	for _, e := range out.Entries {
		fmt.Fprintf(w, "%-24s %-10s %s\n", e.CIDR, e.Source, e.Label)
	}
	return nil
}

func runSecurityBadDestinationsAdd(cmd *cobra.Command, args []string) error {
