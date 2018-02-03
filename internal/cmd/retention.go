package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium retention policy|hold|mode|report` (#2875, #2876, #2877;
// slices A to C of #2576): thin clients over RetentionService. They store the
// policy, the holds and the mode, and print what a sweep would remove or did
// remove. The daemon deletes only while the mode is ENFORCE.

// retentionAPI is the slice of the typed clients these commands use.
type retentionAPI interface {
	GetRetentionPolicy() (*pb.GetRetentionPolicyResponse, error)
	SetRetentionPolicy(req *pb.SetRetentionPolicyRequest) (*pb.SetRetentionPolicyResponse, error)
	ListLegalHolds() (*pb.ListLegalHoldsResponse, error)
	SetLegalHold(req *pb.SetLegalHoldRequest) (*pb.SetLegalHoldResponse, error)
	ReleaseLegalHold(orgID string) (*pb.ReleaseLegalHoldResponse, error)
	GetRetentionReport(refresh bool) (*pb.GetRetentionReportResponse, error)
	GetRetentionMode() (*pb.GetRetentionModeResponse, error)
	SetRetentionMode(req *pb.SetRetentionModeRequest) (*pb.SetRetentionModeResponse, error)
}

// newRetentionAPI builds the client from the global --server/--http flags. A
// variable so tests can substitute a fake.
var newRetentionAPI = func() (retentionAPI, func(), error) {
	if serverAddr == "" {
		return nil, nil, errors.New("--server is required for retention commands")
	}
	if httpMode {
		h, err := client.NewHTTPClient(serverAddr, authToken)
		if err != nil {
			return nil, nil, err
		}
		return h, func() { _ = h.Close() }, nil
	}
	g, err := client.NewGRPCClient(serverAddr, certsDir, insecure)
	if err != nil {
		return nil, nil, err
	}
	return g, func() { _ = g.Close() }, nil
}

// retentionClassFlags maps each per-class flag onto its class, in enum order.
var retentionClassFlags = []struct {
	flag  string
	class pb.RetentionClass
}{
	{"prompts", pb.RetentionClass_RETENTION_CLASS_PROMPTS},
	{"responses", pb.RetentionClass_RETENTION_CLASS_RESPONSES},
	{"gateway-logs", pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS},
	{"session-transcripts", pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS},
	{"evidence-bundles", pb.RetentionClass_RETENTION_CLASS_EVIDENCE_BUNDLES},
}

func retentionClassName(c pb.RetentionClass) string {
	for _, f := range retentionClassFlags {
		if f.class == c {
			return f.flag
		}
	}
	return c.String()
}

var retentionCmd = &cobra.Command{
	Use:   "retention",
	Short: "Retention windows per data class and legal holds per org (admin only; talks to the daemon)",
	Long: `Read and set how long each class of customer data is kept, place or
release legal holds, switch the sweeper between dry-run and enforce, and see
what a sweep would remove or did remove. The daemon starts in dry-run, where
the sweeper only counts and nothing is deleted; "retention mode set --mode
enforce" makes it delete (see docs/security/DATA-FLOW-AND-RETENTION.md).

Every command needs the admin role. A change writes an audit entry naming the
caller and is refused if that entry cannot be written. A daemon with no audit
store (no Postgres, REST disabled, or an audit store that failed to start)
accepts the change and keeps no audit entry.`,
}

var retentionPolicyCmd = &cobra.Command{Use: "policy", Short: "Read or set the retention windows"}

var retentionPolicyGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the window of every data class",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return withRetentionAPI(func(api retentionAPI) error {
			resp, err := api.GetRetentionPolicy()
			if err != nil {
				return err
			}
			p := resp.GetPolicy()
			out := cmd.OutOrStdout()
			for _, w := range p.GetWindows() {
				suffix := ""
				if w.GetIsDefault() {
					suffix = " (default)"
				}
				fmt.Fprintf(out, "%-20s %d days%s\n", retentionClassName(w.GetClass()), w.GetDays(), suffix)
			}
			fmt.Fprintf(out, "Policy revision %d", p.GetRevision())
			if p.GetUpdatedBy() != "" {
				fmt.Fprintf(out, ", set by %s", p.GetUpdatedBy())
			}
			_, err = fmt.Fprintln(out, "\nWindows apply when the mode is enforce (see `retention mode get` and `retention report`).")
			return err
		})
	},
}

var retentionPolicySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the window, in days, of one or more classes (admin only)",
	Long: `Set the window of the classes given as flags; the other classes keep theirs.

Examples:
  containarium retention policy set --prompts 14 --server <host>:50051
  containarium retention policy set --gateway-logs 180 --evidence-bundles 730`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		req := &pb.SetRetentionPolicyRequest{}
		for _, f := range retentionClassFlags {
			if !cmd.Flags().Changed(f.flag) {
				continue
			}
			days, err := cmd.Flags().GetInt32(f.flag)
			if err != nil {
				return err
			}
			req.Windows = append(req.Windows, &pb.RetentionWindow{Class: f.class, Days: days})
		}
		if len(req.Windows) == 0 {
			names := make([]string, len(retentionClassFlags))
			for i, f := range retentionClassFlags {
				names[i] = "--" + f.flag
