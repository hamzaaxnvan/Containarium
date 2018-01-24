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
	Short: "Mark an open finding as resolved",
	Args:  cobra.ExactArgs(1),
	RunE:  runSecurityFindingsResolve,
}

// The list and resolve responses are decoded into the generated
// pb.ListFindingsResponse / pb.ResolveFindingResponse with protojson (the
// grpc-gateway's own encoding), so enums are typed values, never strings
// with a prefix trimmed off (#2768).

// enumLabel is an enum value's name without its type prefix, e.g.
// THREAT_RULE_ID_MINER_SIGNATURE -> MINER_SIGNATURE.
func enumLabel(name, prefix string) string { return strings.TrimPrefix(name, prefix) }

// describeResponse is the one-line text rendering of a response, e.g.
// `response ALERT NONE on box-a, process killed (pid 25872, kills 3)` or
// `response THROTTLE APPLIED on box-b, kill skipped: CONTAINER_INIT` (no
// `on <box>` for a response recorded before it named one).
func describeResponse(r *pb.FindingResponse) string {
	detail := fmt.Sprintf("response %s %s", enumLabel(r.GetAction().String(), "PROCESS_ACTION_"), enumLabel(r.GetState().String(), "RESPONSE_STATE_"))
	if r.GetContainer() != "" {
		detail += " on " + r.GetContainer()
	}
	if r.GetProcessKilled() {
		detail += ", process killed"
		if r.GetKilledPid() != 0 {
			detail += fmt.Sprintf(" (pid %d, kills %d)", r.GetKilledPid(), r.GetKills())
		}
	}
	if r.GetKillSkipReason() != pb.KillSkipReason_KILL_SKIP_REASON_UNSPECIFIED {
		detail += ", kill skipped: " + enumLabel(r.GetKillSkipReason().String(), "KILL_SKIP_REASON_")
	}
	return detail
}

// timestampText renders an optional timestamp as RFC 3339 UTC, "" when unset.
func timestampText(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

func runSecurityFindingsList(cmd *cobra.Command, _ []string) error {
	if serverAddr == "" {
		return errServerRequired()
	}
	q := url.Values{}
	if findingsSeverity != "" {
		q.Set("severity", "THREAT_SEVERITY_"+strings.ToUpper(findingsSeverity))
	}
	if findingsTenant != "" {
		q.Set("tenant_id", findingsTenant)
	}
	if findingsSince != "" {
		q.Set("since", findingsSince)
	}
	if findingsState != "" {
