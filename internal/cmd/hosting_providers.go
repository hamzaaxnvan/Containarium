//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/hosting"
	"github.com/spf13/cobra"
)

var hostingProvidersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List supported DNS providers",
	Long: `List all supported DNS providers for hosting setup.

Each provider requires specific API credentials. Use the --help flag
with the setup command to see credential requirements for each provider.`,
	Run: runHostingProviders,
}

func init() {
	hostingCmd.AddCommand(hostingProvidersCmd)
}

func runHostingProviders(cmd *cobra.Command, args []string) {
	providers := hosting.SupportedProviders()

	fmt.Println("Supported DNS Providers:")
	fmt.Println()

	providerInfo := map[string]struct {
		name    string
		envVars []string
