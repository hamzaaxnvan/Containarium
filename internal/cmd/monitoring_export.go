package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// metricsExportProvider backs the --provider flag on `monitoring export
// enable`. #1069 only ships GCP; AWS is accepted here so the CLI's
// error message ("not yet implemented") comes from the same server
// validation the RPC enforces, rather than a second copy of the
// allow-list living in the CLI.
var metricsExportProvider string

// metricsExportGroups backs the --groups flag on `monitoring export
// enable` (#1081): a comma-separated list of metric groups to enable
// (host, container, platform). Omitted keeps today's behavior — the
// server defaults an empty selection to host.
var metricsExportGroups string

var monitoringExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Enable, disable, or inspect cloud-native metrics export",
	Long: `Opt-in export of host/container infra metrics to the host cloud's
native monitoring (GCP Cloud Monitoring in the MVP). Disabled by
default, reversible any time. Enabling probes the host's credentials
before persisting anything — on GCP this resolves Application Default
Credentials with the monitoring-write scope; a host with no usable ADC
fails with an actionable error and nothing is enabled or exported.

Examples:
  containarium monitoring export enable --provider gcp
  containarium monitoring export status
  containarium monitoring export disable`,
}

var monitoringExportEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable cloud-native metrics export",
	Long: `Enable opt-in export to the given cloud provider's native
monitoring. Requires --provider. The daemon probes the host's
credentials synchronously before persisting anything — on GCP this
resolves Application Default Credentials (ADC) with the
monitoring-write scope; failure returns an actionable error (with an
IAM remediation hint) and nothing is enabled. Takes effect immediately,
no daemon restart required.

Groups (#1081) select which independently-billed series sets export.
Omit --groups to keep host-only (the default); name others to opt in:

  containarium monitoring export enable --provider gcp
  containarium monitoring export enable --provider gcp --groups host,platform`,
	Args: cobra.NoArgs,
	RunE: runMetricsExportEnable,
}

var monitoringExportDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable cloud-native metrics export",
	Long: `Disable cloud-native metrics export. Emission stops within one
export interval; no daemon restart required.

Examples:
  containarium monitoring export disable`,
	Args: cobra.NoArgs,
	RunE: runMetricsExportDisable,
}

var monitoringExportStatusCmd = &cobra.Command{
