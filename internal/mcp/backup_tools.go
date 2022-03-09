package mcp

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// backupTools is the MCP-side catalog for the database-backup feature.
// Defined as a function (not a file-scope slice literal) so the
// registration list in tools.go can pull it in via backupTools() — keeps
// tools.go's slice literal manageable.
//
// Per CLAUDE.md: every tool here is a thin wrapper over the same REST
// endpoint the CLI's `containarium backup` subcommands call (the generated
// BackupService gateway). No agent-only code path.
func backupTools() []Tool {
	tools := []Tool{
		{
			Name: "create_backup",
			Description: "Back up a tenant's database off-host. Runs pg_dump inside " +
				"the tenant's container and stores the compressed dump in a host " +
				"backup directory (dest 'local') or a GCS bucket (dest 'gcs'). The " +
				"point is off-host durability — a dump never shares a failure " +
				"domain with the database it protects. Returns the backup ID, " +
				"size, and SHA-256. Two opt-in options: 'hook' runs the tenant's " +
				"own in-container program and stores its stdout instead of running " +
				"pg_dump (no DB credential needed; the dump is opaque and not " +
				"auto-restorable unless 'hook_format' declares it pg_custom); 'age_recipient' encrypts the dump to a user-held " +
				"age public key before storage, so the platform only holds " +
				"ciphertext. 'key_mode' selects who holds the key on a daemon " +
				"with a backup KMS key configured ('managed': a per-backup key " +
				"wrapped by the KMS; 'both': managed plus the age recipient). 'require_encryption' refuses " +
				"a backup that would be stored unencrypted. " +
				"Mirrors `containarium backup create`.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Tenant whose container holds the database.",
					},
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Logical database name to dump.",
					},
					"dest": map[string]interface{}{
						"type":        "string",
						"description": "Destination: 'local' (host backup dir) or 'gcs'. Default 'local'.",
						"enum":        []string{"local", "gcs"},
					},
					"gcs_bucket": map[string]interface{}{
						"type":        "string",
						"description": "GCS bucket/prefix for dest 'gcs', e.g. 'gs://my-backups/pg'. Required when dest=gcs.",
					},
					"db_user": map[string]interface{}{
						"type":        "string",
						"description": "Postgres role. Default 'postgres'.",
					},
					"db_password": map[string]interface{}{
						"type":        "string",
						"description": "Postgres password. Omit for peer/trust auth. Passed via PGPASSWORD inside the container, never on argv.",
					},
					"hook": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path of an executable inside the container whose stdout is the dump. Bypasses pg_dump and the db_* fields; no credential crosses to the platform. Bare path, no arguments.",
					},
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Label for a hook backup, used in the backup id (default: the hook's basename).",
					},
					"hook_format": map[string]interface{}{
						"type":        "string",
						"description": "Declared format of the hook's output. 'opaque' (default): stored and fetched, never restored or restore-tested by the platform. 'pg_custom': the hook emits a pg_dump -Fc archive, so the backup can be restore-tested (verify_backup) and restored into a named target other than its source. A promise about the hook's output; the daemon does not check it at create time. Only with 'hook'.",
						"enum":        []string{"opaque", "pg_custom"},
					},
					"age_recipient": map[string]interface{}{
						"type":        "string",
						"description": "age public key (age1...) to encrypt the dump to before it is stored, overriding any recipient the tenant has already self-registered as the CONTAINARIUM_BACKUP_AGE_RECIPIENT secret (set_secret). Omit this to use the registered one automatically, or omit both for a plaintext dump. Restore then requires the matching identity file, which the platform never holds.",
					},
					"key_mode": map[string]interface{}{
						"type":        "string",
						"description": "Who holds the key (#2402). 'managed': encrypt to a fresh per-backup identity whose secret the daemon wraps with its KMS key and stores on the record; 'both': managed plus the age recipient, so either opens the file; 'age_recipient': the recipient only, no wrapping. Omit for the daemon default (managed/both when a backup KMS key is configured, otherwise the recipient/plaintext behaviour). A mode the daemon cannot provide is refused, not downgraded. The daemon never unwraps: restore/verify still take the identity, unwrapped under your own KMS credentials.",
						"enum":        []string{"age_recipient", "managed", "both"},
					},
					"require_encryption": map[string]interface{}{
						"type":        "boolean",
						"description": "Refuse the backup (FAILED_PRECONDITION, before any container is touched, so nothing is stored) if it would be stored unencrypted: no age_recipient given or registered for the tenant and no managed backup key on the daemon. Default false. Mirrors `containarium backup create --require-encryption`.",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleCreateBackup,
		},
		{
			Name: "list_backups",
			Description: "List stored database backups (newest first), optionally " +
				"filtered by tenant. Returns ID, database, timestamp, size, " +
				"destination, and location for each. Mirrors `containarium backup list`.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Optional tenant filter. Omit for all tenants (admin).",
					},
				},
			},
			Handler: handleListBackups,
