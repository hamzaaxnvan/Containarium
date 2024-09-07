// `containarium security scan-opt-in` — record, per container, whether the
// intrusive scanners (OpenVAS) may scan it (#2426). CLI-first per the repo
// convention; the decision is a consent record (who, when, why) that the
// scanner reads before every scan, and each change lands in the audit log.
package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// pentestScannerOpenVAS is the REST/JSON name of PentestScanner.OPENVAS.
const pentestScannerOpenVAS = "PENTEST_SCANNER_OPENVAS"

var scanOptInReason string

var securityScanOptInCmd = &cobra.Command{
	Use:   "scan-opt-in",
	Short: "Record whether a container may be scanned by OpenVAS",
	Long: `Records the scan decision for a container. Scanning with OpenVAS is
opt-in: a container is scanned only if its owner (or an operator) allowed it,
or the operator listed it in CONTAINARIUM_OPENVAS_OPT_IN. A recorded refusal
overrides that list, so an owner can always say no.`,
}

var securityScanOptInAllowCmd = &cobra.Command{
	Use:   "allow <container>",
	Short: "Allow OpenVAS to scan a container",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return runScanOptInSet(cmd, args[0], true) },
}

var securityScanOptInRefuseCmd = &cobra.Command{
	Use:   "refuse <container>",
	Short: "Refuse OpenVAS scans of a container (overrides the operator-wide list)",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return runScanOptInSet(cmd, args[0], false) },
}

var scanOptInListJSONOut bool

var securityScanOptInListCmd = &cobra.Command{
	Use:   "list [container]",
	Short: "List recorded scan decisions (all of them needs admin)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runScanOptInList,
}

// scanOptInSetBody is the request body of PUT /v1/pentest/scan-opt-ins/{name}.
type scanOptInSetBody struct {
	Scanner string `json:"scanner"`
	OptedIn bool   `json:"optedIn"`
	Reason  string `json:"reason"`
}

// scanOptInEnvelope mirrors PentestScanOptIn's grpc-gateway JSON shape.
type scanOptInEnvelope struct {
	ContainerName string `json:"containerName"`
	Scanner       string `json:"scanner"`
	OptedIn       bool   `json:"optedIn"`
	Reason        string `json:"reason"`
	UpdatedBy     string `json:"updatedBy"`
	UpdatedAt     string `json:"updatedAt"`
}

type setScanOptInEnvelope struct {
	OptIn scanOptInEnvelope `json:"optIn"`
}

