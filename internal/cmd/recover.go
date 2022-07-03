//go:build !windows && !containarium_client

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/collaborator"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// RecoveryConfig holds the configuration for disaster recovery
type RecoveryConfig struct {
	// Network configuration
	NetworkName string `yaml:"network_name"`
	NetworkCIDR string `yaml:"network_cidr"`

	// Storage configuration
	StoragePoolName string `yaml:"storage_pool_name"`
	StorageDriver   string `yaml:"storage_driver"`
	ZFSSource       string `yaml:"zfs_source"`

	// Daemon configuration (for restoring systemd service)
	DaemonFlags DaemonConfig `yaml:"daemon"`
}

// DaemonConfig holds daemon startup flags
type DaemonConfig struct {
	Address       string `yaml:"address"`
	Port          int    `yaml:"port"`
	HTTPPort      int    `yaml:"http_port"`
	BaseDomain    string `yaml:"base_domain"`
	CaddyAdminURL string `yaml:"caddy_admin_url"`
	JWTSecretFile string `yaml:"jwt_secret_file"`
	AppHosting    bool   `yaml:"app_hosting"`
	SkipInfraInit bool   `yaml:"skip_infra_init"`
}

// DefaultRecoveryConfigPath is the default path for recovery config on persistent storage
const DefaultRecoveryConfigPath = "/mnt/incus-data/containarium-recovery.yaml"

var (
	recoverConfigFile    string
	recoverNetworkCIDR   string
	recoverNetworkName   string
	recoverStoragePool   string
	recoverStorageDriver string
	recoverZFSSource     string
	recoverDryRun        bool
	recoverSkipAccounts  bool
	recoverStartAll      bool
)

var recoverCmd = &cobra.Command{
	Use:   "recover",
	Short: "Recover containers and settings after instance recreation",
	Long: `Recover containers, network, and SSH accounts after the jump server is recreated.

This command performs disaster recovery when the boot disk is lost but containers
persist on the external ZFS storage pool. It handles:

1. Network creation (incusbr0 with correct CIDR)
2. Storage pool import via 'incus admin recover'
3. Starting all recovered containers
4. Syncing jump server SSH accounts from container keys

Two modes are supported:

EXPLICIT MODE - Specify all parameters via flags:
  containarium recover \
    --network-cidr 10.0.3.1/24 \
    --zfs-source incus-pool/containers

CONFIG MODE - Load from config file on persistent disk:
  containarium recover --config /mnt/incus-data/containarium-recovery.yaml

The config file is automatically saved during normal daemon operation,
so it survives instance recreation when stored on persistent disk.

Examples:
  # Recover using config file (recommended)
  containarium recover --config /mnt/incus-data/containarium-recovery.yaml

  # Recover with explicit parameters
  containarium recover \
    --network-name incusbr0 \
    --network-cidr 10.0.3.1/24 \
    --storage-pool default \
    --storage-driver zfs \
    --zfs-source incus-pool/containers

  # Dry run - show what would be done
  containarium recover --config /mnt/incus-data/containarium-recovery.yaml --dry-run

  # Skip SSH account sync
  containarium recover --config /mnt/incus-data/containarium-recovery.yaml --skip-accounts`,
	RunE: runRecover,
}

func init() {
	rootCmd.AddCommand(recoverCmd)

	recoverCmd.Flags().StringVar(&recoverConfigFile, "config", "", "Path to recovery config file (default: "+DefaultRecoveryConfigPath+")")
	recoverCmd.Flags().StringVar(&recoverNetworkName, "network-name", "incusbr0", "Name of the network bridge to create")
	recoverCmd.Flags().StringVar(&recoverNetworkCIDR, "network-cidr", "", "Network CIDR (e.g., 10.0.3.1/24)")
	recoverCmd.Flags().StringVar(&recoverStoragePool, "storage-pool", "default", "Name of the storage pool")
	recoverCmd.Flags().StringVar(&recoverStorageDriver, "storage-driver", "zfs", "Storage driver (zfs, btrfs, dir, lvm)")
	recoverCmd.Flags().StringVar(&recoverZFSSource, "zfs-source", "", "ZFS dataset source (e.g., incus-pool/containers)")
	recoverCmd.Flags().BoolVar(&recoverDryRun, "dry-run", false, "Show what would be done without making changes")
	recoverCmd.Flags().BoolVar(&recoverSkipAccounts, "skip-accounts", false, "Skip SSH account sync")
	recoverCmd.Flags().BoolVar(&recoverStartAll, "start-all", true, "Start all containers after recovery")
}

func runRecover(cmd *cobra.Command, args []string) error {
	fmt.Println("==============================================")
	fmt.Println("  Containarium Disaster Recovery")
	fmt.Println("==============================================")
	fmt.Println()

	// Load configuration
	config, err := loadRecoveryConfig()
	if err != nil {
		return fmt.Errorf("failed to load recovery config: %w", err)
	}

	if recoverDryRun {
		fmt.Println("[DRY RUN MODE - No changes will be made]")
		fmt.Println()
	}

	// Print configuration
	fmt.Println("Recovery Configuration:")
	fmt.Printf("  Network:      %s (%s)\n", config.NetworkName, config.NetworkCIDR)
	fmt.Printf("  Storage Pool: %s (driver: %s)\n", config.StoragePoolName, config.StorageDriver)
	fmt.Printf("  ZFS Source:   %s\n", config.ZFSSource)
	fmt.Println()

	// Step 1: Create network
	if err := recoverNetwork(config); err != nil {
		return fmt.Errorf("network recovery failed: %w", err)
	}

	// Step 2: Recover storage pool and containers
	if err := recoverStorage(config); err != nil {
		return fmt.Errorf("storage recovery failed: %w", err)
	}

	// Step 3: Add network device to default profile
	if err := recoverDefaultProfile(config); err != nil {
		return fmt.Errorf("profile recovery failed: %w", err)
	}

	// Step 4: Start all containers
	if recoverStartAll {
		if err := startAllContainers(); err != nil {
			return fmt.Errorf("failed to start containers: %w", err)
		}
	}

	// Step 5: Sync SSH accounts
	if !recoverSkipAccounts {
		if err := syncSSHAccounts(); err != nil {
			// Don't fail completely if account sync fails
			fmt.Printf("Warning: SSH account sync had issues: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("  Recovery Complete!")
	fmt.Println("==============================================")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Restart containarium daemon: sudo systemctl restart containarium")
	fmt.Println("  2. Reload Caddy if needed")
	fmt.Println("  3. Verify SSH jump access works")
	fmt.Println()

	return nil
}

func loadRecoveryConfig() (*RecoveryConfig, error) {
	config := &RecoveryConfig{
		NetworkName:     recoverNetworkName,
		NetworkCIDR:     recoverNetworkCIDR,
		StoragePoolName: recoverStoragePool,
		StorageDriver:   recoverStorageDriver,
		ZFSSource:       recoverZFSSource,
	}

	// If config file is specified or exists at default path, load it
	configPath := recoverConfigFile
	if configPath == "" {
		// Check if default config exists
		if _, err := os.Stat(DefaultRecoveryConfigPath); err == nil {
			configPath = DefaultRecoveryConfigPath
		}
	}

	if configPath != "" {
		fmt.Printf("Loading config from: %s\n", configPath)
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Command-line flags override config file
	if recoverNetworkCIDR != "" {
		config.NetworkCIDR = recoverNetworkCIDR
	}
	if recoverZFSSource != "" {
		config.ZFSSource = recoverZFSSource
	}

	// Validate required fields
	if config.NetworkCIDR == "" {
		return nil, fmt.Errorf("network CIDR is required (--network-cidr or in config file)")
	}
	if config.ZFSSource == "" {
		return nil, fmt.Errorf("ZFS source is required (--zfs-source or in config file)")
	}

	return config, nil
}

func recoverNetwork(config *RecoveryConfig) error {
	fmt.Printf("Step 1: Creating network %s...\n", config.NetworkName)

	// Check if network already exists
	checkCmd := exec.Command("incus", "network", "show", config.NetworkName)
	if err := checkCmd.Run(); err == nil {
		fmt.Printf("  Network %s already exists, skipping\n", config.NetworkName)
		return nil
	}

	if recoverDryRun {
		fmt.Printf("  [DRY RUN] Would create network %s with CIDR %s\n", config.NetworkName, config.NetworkCIDR)
		return nil
	}

	// Create the network
	createCmd := exec.Command("incus", "network", "create", config.NetworkName,
		fmt.Sprintf("ipv4.address=%s", config.NetworkCIDR),
		"ipv4.nat=true",
