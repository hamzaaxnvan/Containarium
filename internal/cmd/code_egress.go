package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/codeegress"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium code egress-policy` (#2378) manages the admin-approved egress
// allowlist for the coding tool's own traffic. With no tenant argument each
// verb acts on the cluster default. set/delete are admin-only; get is for an
// admin or the tenant itself holding code-egress:read. The policy is stored
// only; nothing enforces it yet.
var codeEgressPolicyCmd = &cobra.Command{
	Use:   "egress-policy",
	Short: "Manage the coding tool's egress allowlist",
	Long: `Manage the coding tool's egress allowlist (#2370).

A tenant policy replaces the cluster default (no tenant argument). With
neither, coding runs are unrestricted. A policy with no CIDRs or domains under
--mode enforce denies every listed destination; the box's DNS resolver and
the model gateway stay allowed and are shown by get.

This is separate from network-policy: it never changes a tenant's own
NetworkPolicy. Requires --server.`,
}

var (
	codeEgressMode    string
	codeEgressCIDRs   []string
	codeEgressDomains []string
	codeEgressJSON    bool
)

var codeEgressPolicyGetCmd = &cobra.Command{
	Use:   "get [tenant]",
	Short: "Show the stored and effective policy (no tenant = cluster default)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		return runCodeEgressGet(os.Stdout, args, codeEgressJSON)
	},
}

var codeEgressPolicySetCmd = &cobra.Command{
	Use:   "set [tenant] --mode log_only|enforce [--egress-cidr C]... [--egress-domain D]...",
	Short: "Create or replace a policy (no tenant = cluster default; admin)",
	Args:  cobra.MaximumNArgs(1),
