package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// backupAgeRecipientSecret is the per-tenant secret a scheduled backup
// encrypts to (#1836). Mirrors the daemon's constant; the name, not a value.
const backupAgeRecipientSecret = "CONTAINARIUM_BACKUP_AGE_RECIPIENT" // #nosec G101 -- secret NAME, not a credential value

// defaultBackupTenantsConf is where backup-all-tenants.sh reads its tenants.
const defaultBackupTenantsConf = "/etc/containarium/backup-tenants.conf"

var backupStatusConf string

var backupStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check that every tenant in the nightly backup config will be encrypted",
	Long: `List the tenants named in backup-tenants.conf (the nightly
backup-all-tenants.sh config) and check, on this daemon, that each has a
CONTAINARIUM_BACKUP_AGE_RECIPIENT secret registered (#3169).

A tenant with no recipient gets its nightly dump stored UNENCRYPTED unless
the daemon has a managed backup key configured; the daemon reports that, and
when it does, tenants with no recipient are listed as OK. Registered secrets are
stored per daemon and do not travel with a copied container, so run this
after moving a tenant to another host, before its first backup there.
Exits non-zero when any tenant has no recipient or could not be checked.

Examples:
  containarium backup status --server <host>
  containarium backup status --conf ./backup-tenants.conf --server <host>`,
	Args: cobra.NoArgs,
	RunE: runBackupStatus,
}

func init() {
	confDefault := os.Getenv("CONTAINARIUM_BACKUP_CONF")
	if confDefault == "" {
		confDefault = defaultBackupTenantsConf
	}
	backupStatusCmd.Flags().StringVar(&backupStatusConf, "conf", confDefault,
		"tenant config read by backup-all-tenants.sh (env CONTAINARIUM_BACKUP_CONF)")
	backupCmd.AddCommand(backupStatusCmd)
}

// backupSecretsLister is the part of the typed client the status check
// needs. Both the gRPC and HTTP clients satisfy it.
type backupSecretsLister interface {
	ListSecrets(username string) ([]*pb.SecretMetadata, error)
}

type backupSecretsClient interface {
	backupSecretsLister
	// ManagedBackupKeyConfigured reports whether the daemon has a managed
	// backup key (SystemInfo.managed_backup_key_configured, #3213).
	ManagedBackupKeyConfigured() (bool, error)
	Close() error
}

// newBackupSecretsClientFn is the seam tests substitute.
var newBackupSecretsClientFn = func() (backupSecretsClient, error) {
	if serverAddr == "" {
		return nil, fmt.Errorf("--server is required")
	}
	if httpMode {
		return client.NewHTTPClient(serverAddr, authToken)
	}
	return client.NewGRPCClient(serverAddr, certsDir, insecure)
}

func runBackupStatus(cmd *cobra.Command, args []string) error {
	tenants, err := readBackupConfTenants(backupStatusConf)
	if err != nil {
		return err
	}
	if len(tenants) == 0 {
		fmt.Printf("No tenants configured in %s.\n", backupStatusConf)
		return nil
	}
	c, err := newBackupSecretsClientFn()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	// Fail safe: if the lookup itself fails (transport error, or the caller
	// may not read system info), the state is Unknown: warn, and an
	// unregistered tenant is reported "encryption not confirmed" and fails.
	// An older daemon does not fail the lookup. It answers successfully
	// with the field absent (proto3 default false), which reads as Absent:
