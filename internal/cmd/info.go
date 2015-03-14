package cmd

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/spf13/cobra"
)

var (
	showMetrics bool
)

var infoCmd = &cobra.Command{
	Use:   "info [username]",
	Short: "Show container or system information",
	Long: `Show detailed information about a specific container or the entire system.

If a username is provided, shows details about that user's container.
If no username is provided, shows system-wide information.

Examples:
  # Show system information
  containarium info

  # Show information for a specific container
  containarium info alice

  # Show container information with metrics
  containarium info alice --metrics`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInfo,
}

func init() {
	rootCmd.AddCommand(infoCmd)

	infoCmd.Flags().BoolVarP(&showMetrics, "metrics", "m", false, "Show resource usage metrics")
}

func runInfo(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		// Show system information
		return showSystemInfo()
	}

	// Show container information
	username := args[0]
	return showContainerInfo(username)
}

func showSystemInfo() error {
	// System info is a host-level view; the hosted control plane has no
	// per-tenant equivalent. Refuse client-side (#456) rather than 404.
	if isCloudTarget(serverAddr, authToken) {
		return errUnsupportedOnCloud("system info", "use `containarium backends` for the fleet view")
	}
	fmt.Println("=== Containarium System Information ===")
	fmt.Println()

	// Get server info and containers - use remote or local mode
	var serverInfo *incus.ServerInfo
	var containers []incus.ContainerInfo
	var err error

	if httpMode && serverAddr != "" {
		// Remote mode via HTTP
		var httpClient *client.HTTPClient
		httpClient, err = client.NewHTTPClient(serverAddr, authToken)
		if err != nil {
			return fmt.Errorf("failed to create HTTP client: %w", err)
		}
		defer func() { _ = httpClient.Close() }()

		serverInfo, err = httpClient.GetSystemInfo()
		if err != nil {
			return fmt.Errorf("failed to get server info: %w", err)
		}

		containers, err = httpClient.ListContainers()
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
	} else if serverAddr != "" {
		// Remote mode via gRPC
		var grpcClient *client.GRPCClient
		grpcClient, err = client.NewGRPCClient(serverAddr, certsDir, insecure)
		if err != nil {
			return fmt.Errorf("failed to connect to remote server: %w", err)
		}
		defer func() { _ = grpcClient.Close() }()

		serverInfo, err = grpcClient.GetSystemInfo()
		if err != nil {
			return fmt.Errorf("failed to get server info: %w", err)
		}

		containers, err = grpcClient.ListContainers()
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}
	} else {
		// Local mode via Incus
		serverInfo, containers, err = getSystemInfoLocal()
		if err != nil {
			return err
		}
	}

	fmt.Println("Incus Information:")
	fmt.Printf("  Version:         %s\n", serverInfo.Version)
	fmt.Printf("  Kernel:          %s\n", serverInfo.KernelVersion)
	fmt.Println()

	if serverInfo.TotalMemoryBytes > 0 {
		fmt.Println("Memory:")
		fmt.Printf("  Total:           %.1f GiB\n", float64(serverInfo.TotalMemoryBytes)/(1<<30))
		fmt.Printf("  Available:       %.1f GiB\n", float64(serverInfo.AvailableMemoryBytes)/(1<<30))
		fmt.Println()
	}
