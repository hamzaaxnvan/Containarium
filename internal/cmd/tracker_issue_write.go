package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// Tracker issue write verbs (#1922). tracker_issue.go's own doc comment
// said these "land in a follow-up story" — the typed client
// (internal/client/{grpc,http}.go) and the platform MCP tools
// (internal/mcp/tracker_tools.go) both got them; this CLI surface did
// not, until now. Gated by tracker:write on the token, same as the RPCs
// themselves.

var trackerIssueCommentCmd = &cobra.Command{
	Use:   "comment <username> <connection> <number> --body <text>",
	Short: "Post a stamped, sanitized comment on an issue or change request",
	Args:  cobra.ExactArgs(3),
	RunE:  runTrackerIssueComment,
}

var trackerIssueClaimCmd = &cobra.Command{
	Use:   "claim <username> <connection> <number>",
	Short: "Claim an issue for the calling run",
	Long: `Posts a stamped claim comment and assigns the calling run if the issue is
unassigned, unless a live or recent claim from a different run already
holds it. Idempotent for the same run.

Example:
  containarium tracker issue claim alice default 42
  containarium tracker issue claim alice default 42 --stale-after 4h`,
	Args: cobra.ExactArgs(3),
	RunE: runTrackerIssueClaim,
}

var trackerIssueLabelCmd = &cobra.Command{
	Use:   "label <username> <connection> <number>",
	Short: "Add and/or remove labels on an issue",
	Long: `Example:
  containarium tracker issue label alice default 42 --add triaged --remove needs-triage`,
	Args: cobra.ExactArgs(3),
	RunE: runTrackerIssueLabel,
}

var trackerIssueCreateCmd = &cobra.Command{
	Use:   "create <username> <connection> --title <text> [--body <text>] [--label <label>]... [--parent <number>]",
	Short: "File a follow-up issue on the tracker",
	Long: `Files an issue on the tracker the named connection points at (#2024).

Labels must pass the connection's label allow-list (set with
"tracker connect --label-allow"; default scope:*, model:*,
agent:needs-approval) — anything else is rejected before the tracker is
touched. A run-scoped token must name --parent: the follow-up is linked
to it, recorded in the issue lineage (bounded by the connection's
--max-depth / --max-children), filed with agent:needs-approval unless
the connection auto-chains, and stamped with the run's identity; the
parent gets one back-link comment.

Example:
  containarium tracker issue create alice default \
    --title "Design: widget API" --body "From the product run." \
    --label scope:architecture --parent 42`,
	Args: cobra.ExactArgs(2),
	RunE: runTrackerIssueCreate,
}

var (
	trackerIssueCommentBody  string
	trackerIssueClaimStale   time.Duration
	trackerIssueLabelAdd     []string
	trackerIssueLabelRemove  []string
	trackerIssueCreateTitle  string
	trackerIssueCreateBody   string
	trackerIssueCreateLabels []string
	trackerIssueCreateParent int64
)

func init() {
	trackerIssueCmd.AddCommand(trackerIssueCreateCmd)
	trackerIssueCreateCmd.Flags().StringVar(&trackerIssueCreateTitle, "title", "", "Issue title (required)")
	trackerIssueCreateCmd.Flags().StringVar(&trackerIssueCreateBody, "body", "", "Issue body")
	trackerIssueCreateCmd.Flags().StringArrayVar(&trackerIssueCreateLabels, "label", nil, "Label to apply (repeat for multiple); must pass the connection's allow-list")
	trackerIssueCreateCmd.Flags().Int64Var(&trackerIssueCreateParent, "parent", 0, "Parent issue number (required for a run-scoped token)")
	_ = trackerIssueCreateCmd.MarkFlagRequired("title")

	trackerIssueCmd.AddCommand(trackerIssueCommentCmd)
	trackerIssueCommentCmd.Flags().StringVar(&trackerIssueCommentBody, "body", "", "Comment text (required)")
	_ = trackerIssueCommentCmd.MarkFlagRequired("body")

	trackerIssueCmd.AddCommand(trackerIssueClaimCmd)
	trackerIssueClaimCmd.Flags().DurationVar(&trackerIssueClaimStale, "stale-after", 0,
