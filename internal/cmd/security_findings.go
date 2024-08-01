// `containarium security findings` — list and resolve security findings
// raised by the threat-detection sentry (#1643). CLI-first per the repo
// convention; the MCP tools (internal/mcp/security.go) wrap the same REST
// calls.
package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// securitySentryFindingsCmd IS the list command (per the #1643 acceptance
// criterion's literal syntax: `containarium security findings [--severity
// --tenant --since]`), with `resolve` as its only child action — cobra
// dispatches to a subcommand when the first arg matches one, and falls back
// to the parent's own RunE (list) otherwise.
var securitySentryFindingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "List and resolve security findings raised by the threat-detection sentry",
	Long: `Lists security findings raised by the background threat-detection sentry
(#1640-#1642), with evidence. Non-admin callers only see their own tenant.`,
	Args: cobra.NoArgs,
	RunE: runSecurityFindingsList,
}

var (
	findingsSeverity string
	findingsTenant   string
	findingsSince    string
	findingsState    string
	findingsLimit    int
	findingsJSONOut  bool
)

// securitySentryFindingsListCmd is an explicit `list` alias for the parent
// command's default action — some operators expect every noun to have a
// spelled-out verb (matches `security bad-destinations list`'s sibling
// shape); both run the identical RunE.
var securitySentryFindingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List security findings, most recently seen first (same as `findings` with no subcommand)",
	Args:  cobra.NoArgs,
	RunE:  runSecurityFindingsList,
}

var securitySentryFindingsResolveCmd = &cobra.Command{
	Use:   "resolve <id>",
