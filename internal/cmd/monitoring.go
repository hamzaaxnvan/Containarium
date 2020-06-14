package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var monitoringCmd = &cobra.Command{
	Use:     "monitoring",
	Short:   "Enable or disable OTel app telemetry on a container",
	Long:    `Manage application-emitted OpenTelemetry on existing containers without recreating them.`,
	Aliases: []string{"monitor", "otel"},
}

var monitoringEnableCmd = &cobra.Command{
	Use:   "enable <username>",
	Short: "Enable OTel monitoring on an existing container",
	Long: `Stamp OTEL_EXPORTER_OTLP_ENDPOINT and related env vars onto the container's
LXC config and restart it so the app picks them up.

Requires the daemon to have an OTel collector endpoint configured
(auto-ensured at daemon startup when VictoriaMetrics is available, or
explicitly via --otel-collector-endpoint).

Examples:
  containarium monitoring enable alice
  containarium monitoring enable --server addr:port alice`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runMonitoringToggle(args[0], true) },
}

var monitoringDisableCmd = &cobra.Command{
	Use:   "disable <username>",
	Short: "Disable OTel monitoring on an existing container",
	Long: `Unset the OTEL_* env vars from the container's LXC config and restart it so
the SDK falls back to its no-endpoint defaults.

Examples:
  containarium monitoring disable alice`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runMonitoringToggle(args[0], false) },
}

func init() {
