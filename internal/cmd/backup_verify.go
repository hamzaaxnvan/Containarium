package cmd

import (
	"fmt"
	"os"

	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	backupVerifyTarget string
	backupVerifyDBUser string
	backupVerifyDBPass string
	backupVerifyDBHost string
	backupVerifyDBPort int32

	// #2295: path to the age identity file for an encrypted record.
	backupVerifyAgeIdentityFile string

	// #2403: managed key and newest-record selection.
	backupVerifyManaged  bool
	backupVerifyLatest   bool
	backupVerifyUser     string
	backupVerifyDatabase string
)

var backupVerifyCmd = &cobra.Command{
	Use:   "verify [<id>]",
	Short: "Restore-test a stored dump against a throwaway container",
	Long: `Prove a backup is restorable, not merely intact.

The recorded SHA-256 shows the dump bytes have not changed. It cannot show
the dump will load: a dump taken against a wedged database, a schema the
target engine cannot read, or a truncated export all hash perfectly well.
Verification answers the question the checksum cannot, by actually
restoring the dump.

The dump is loaded into a throwaway database inside the --target
container, sanity-checked, and the scratch database is dropped again. The
source container is never touched; the verification engine always refuses
to use it as the restore target.

Set --target to the backup's own tenant username to have the daemon create
an isolated Postgres 16 verification container and delete it after the test.
This mode uses daemon-generated local connection settings; --db-* flags
apply only to an explicitly selected different target tenant. A tenant-bound
