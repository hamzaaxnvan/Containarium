package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	backupRestoreClean    bool
	backupRestoreDatabase string
	backupRestoreDBUser   string
	backupRestoreDBPass   string
	backupRestoreDBHost   string
	backupRestoreDBPort   int32

	// #1831: path to the age identity file for an encrypted record.
	backupRestoreAgeIdentityFile string

	// #2403: managed key, newest-record selection, and restore target.
	backupRestoreManaged bool
	backupRestoreLatest  bool
	backupRestoreUser    string
	backupRestoreTarget  string
)

var backupRestoreCmd = &cobra.Command{
	Use:   "restore [<id>]",
	Short: "Restore a stored dump back into its container's database",
	Long: `Stream a stored dump back into the owning tenant's container database
via pg_restore. The dump is integrity-checked (SHA-256) before it touches
the database.

DESTRUCTIVE with --clean: pg_restore is run with --clean --if-exists,
dropping objects before recreating them. Without --clean, restore loads
into the existing database and errors on conflicting objects.

By default the dump is restored into the database it was taken from;
override with --database to restore into a different one. By default it
is restored into the backup's own container; --target <name>-container
restores into another tenant container instead (you must be authorized
for that tenant too).

Name the backup by id, or pass --latest with --user and --database to
restore the newest backup of that tenant's database.

An encrypted backup (created with --age-recipient) needs the matching age
identity: pass the key file with --age-identity-file. The platform holds
no decryption key, so a restore without it is refused. The identity is
read from the file (never argv), used for this one call, and not stored.

` + managedHelp + `

A hook backup (engine "hook") with the default opaque format is a stream
the platform never understood and cannot restore: fetch the object and
apply it with the tenant's tooling. A hook declared --hook-format
pg_custom can be restored, but only into a --target other than its
source container.

Examples:
  containarium backup restore alice-app-20260605T130405Z --clean --server <host>
  containarium backup restore alice-app-20260605T130405Z --database app_staging --server <host>
  containarium backup restore alice-app-20260605T130405Z --age-identity-file backup.key --clean --server <host>
  containarium backup restore --managed --latest --user alice --database app \
      --target scratch-container --server <host>`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBackupRestore,
}

func init() {
	backupCmd.AddCommand(backupRestoreCmd)
	f := backupRestoreCmd.Flags()
	f.BoolVar(&backupRestoreClean, "clean", false, "pass --clean --if-exists to pg_restore (drops objects first)")
	f.StringVar(&backupRestoreDatabase, "database", "", "target database (default: the backup's own database)")
	f.StringVar(&backupRestoreDBUser, "db-user", "", "Postgres role (default: postgres)")
	f.StringVar(&backupRestoreDBPass, "db-password", "", "Postgres password (omit for peer/trust auth)")
	f.StringVar(&backupRestoreDBHost, "db-host", "", "DB host as seen inside the container (default: 127.0.0.1)")
	f.Int32Var(&backupRestoreDBPort, "db-port", 0, "DB port (default: 5432)")
	f.StringVar(&backupRestoreAgeIdentityFile, "age-identity-file", "", "age identity file (AGE-SECRET-KEY-1...) that decrypts an encrypted backup; required for records created with --age-recipient")
	f.BoolVar(&backupRestoreManaged, "managed", false, "unwrap the record's managed key with your own cloud credentials (audited by the KMS) instead of --age-identity-file")
	f.BoolVar(&backupRestoreLatest, "latest", false, "restore the newest backup for --user and --database instead of a named id")
	f.StringVar(&backupRestoreUser, "user", "", "tenant whose newest backup --latest selects")
	f.StringVar(&backupRestoreTarget, "target", "", "container to restore into, <username>-container (default: the backup's own container)")
}

// parseAgeIdentity extracts the single AGE-SECRET-KEY-1… line from an
