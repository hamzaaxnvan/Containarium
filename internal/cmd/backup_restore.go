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
