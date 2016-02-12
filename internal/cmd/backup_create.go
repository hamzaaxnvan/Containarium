package cmd

import (
	"fmt"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	backupCreateDatabase string
	backupCreateDest     string
	backupCreateBucket   string
	backupCreateDBUser   string
	backupCreateDBPass   string
	backupCreateDBHost   string
	backupCreateDBPort   int32

	// #1831
	backupCreateHook         string
	backupCreateLabel        string
	backupCreateAgeRecipient string

	// #2402
	backupCreateKeyMode string
	// #2405
	backupCreateHookFormat string
	// #2801
	backupCreateBox bool
	// #3169
	backupCreateRequireEncryption bool
)

var backupCreateCmd = &cobra.Command{
	Use:   "create <username>",
	Short: "Dump a container's database(s), or the whole box, and store them off-host",
	Long: `Run pg_dump inside the tenant's container and store the compressed
dump(s) at the chosen destination.

Omit --database to back up EVERY non-template database found in the
container — the default. This is usually what you want: no need to
already know a database name, and there's no name to get wrong. Pass
--database to dump just that one instead.

Connection defaults target a per-container local Postgres on loopback
(user "postgres", host 127.0.0.1, port 5432). The password, if needed, is
passed to pg_dump via PGPASSWORD inside the container — never on argv.

Credential-less hook mode (--hook): instead of pg_dump, run the tenant's
own program inside the container and capture its stdout as the dump. No
DB credential crosses to the platform, and databases the platform can't
reach directly (e.g. nested inside an in-container Docker stack) become
backup-able. The path must be absolute with no arguments. Hook dumps are
opaque by default: stored, listed and fetched, but never auto-restored.

If the hook emits a 'pg_dump -Fc' custom-format archive, say so with
--hook-format pg_custom. The backup can then be restore-tested with
'backup verify', and restored into a named target container other than
its source (never in place). This is a promise about the hook's output;
the daemon does not check it when the backup is taken.

User-held encryption (--age-recipient): encrypt the dump to an age public
key before it is staged or uploaded, so the daemon's disk, the object
store and the operator only ever hold ciphertext. Restore then needs the
matching identity (see 'backup restore --age-identity-file').

For a SCHEDULED backup, a tenant can register its recipient once instead
of passing --age-recipient on every run:
  containarium secrets set alice CONTAINARIUM_BACKUP_AGE_RECIPIENT age1...
A create with no --age-recipient flag then encrypts to that registered
key automatically; an explicit --age-recipient still overrides it for a
one-off call. Neither a tenant with no key registered nor a standalone
daemon (no secrets store) is an error — both mean plaintext, as today:
the dump is stored unencrypted, the output says "Encrypted: no" with a
WARNING, and the record carries encrypted: false. Pass
