//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// Phase 4.5 follow-up — operator-facing audit CLI. The
// runbook references `containarium audit query` and
// `audit verify`; this file makes those commands real
// (they weren't shipped before).
//
// Both commands take the direct Postgres path rather than
// going through the daemon's REST API. Rationale: the
// daemon's audit-log routes are read-only on the wire and
// audit retention is a maintenance task, not a runtime
// API. Operators with DB credentials run these locally
// alongside the migration tools.

var (
	// `audit query` flags
	auditQueryUsername string
	auditQueryAction   string
	auditQueryResource string
	auditQueryRunID    string
	auditQueryFrom     string
	auditQueryTo       string
	auditQueryLimit    int
	auditQueryRaw      bool

	// `audit verify` flags
	auditVerifyFromID int64
	auditVerifyBatch  int

	// `audit verify-anchor` flags
	auditAnchorPath   string
	auditAnchorBucket string
	// auditAnchorTSATrustBundle enables the RFC 3161 token check (#2728).
	auditAnchorTSATrustBundle string
)

// defaultAuditAnchorPath mirrors internal/server's constant of the same
// name (#1706) — duplicated rather than imported so this CLI keeps its
// stated direct-Postgres-only dependency shape rather than pulling in the
// daemon package for one path string.
const defaultAuditAnchorPath = "/var/lib/containarium/audit-anchors.jsonl"

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Inspect and verify the audit log",
	Long: `Query historical audit-log entries and verify the tamper-evidence
hash chain.

Direct Postgres access — needs CONTAINARIUM_POSTGRES_URL /
_URL_FILE / _PASSWORD_FILE configured the same way the daemon
reads it. See the operator runbook for the full DSN options.`,
}

var auditQueryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query audit log entries with filters",
	Long: `Returns audit log rows matching the given filters, most-recent
first. Filters compose with AND — leave flags unset to match
everything in that dimension.

The timestamp / action / username / resource / run id columns
are indexed, so even an unfiltered query against a
multi-million-row table returns within a few seconds.`,
	Example: `  # Everything an admin did in the last hour
  containarium audit query --username ops \
      --from "$(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%SZ)" \
      --limit 100

  # All token revocations this month
  containarium audit query --action token_revoke \
      --from "$(date -u -d '30 days ago' +%Y-%m-%dT%H:%M:%SZ)"

  # Tab-separated for grep/awk piping
  containarium audit query --action create_container --raw | awk '{print $2}'`,
	RunE: runAuditQuery,
}

var auditVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify the audit-log tamper-evidence hash chain",
	Long: `Walks the audit log's row_hash chain forward from --from-id
(or the chain start by default) and reports the first row
whose hash doesn't match its stored value. A break indicates
the row was edited after insert OR a row was inserted
between two existing rows.

The chain doesn't prove the log is COMPLETE (an attacker
with database access could rewrite a row and recompute every
hash after it, or delete the tail outright — either preserves
internal consistency). It proves nothing has been MODIFIED or
INSERTED as long as nobody can also rewrite the rows after the
edit. See 'audit verify-anchor' for the check that catches a
privileged rewrite-and-recompute, which this command cannot.

A clean verify reports "intact" and the highest ID seen.`,
	Example: `  # Verify from the chain start
  containarium audit verify

  # Verify from a specific ID (faster on large tables)
  containarium audit verify --from-id 100000

  # Smaller batch size for low-memory hosts
  containarium audit verify --batch 200`,
	RunE: runAuditVerify,
}

var auditVerifyAnchorCmd = &cobra.Command{
	Use:   "verify-anchor",
	Short: "Verify the audit chain against its last externally-anchored root",
	Long: `'audit verify' proves internal consistency: every row's hash matches
what its own fields (and the previous row's hash) compute to.
It CANNOT catch an operator with Postgres write access who
edits a row and then correctly recomputes every hash after it
— that produces a chain that is internally consistent by
construction, because the attacker used the same hashing rule
the daemon does.

This command closes that gap (#1706). The daemon periodically
publishes the chain's current tip to an external file the
database cannot rewrite (see CONTAINARIUM_AUDIT_ANCHOR_PATH /
CONTAINARIUM_AUDIT_ANCHOR_INTERVAL on the daemon). This command
re-reads the row at that anchored checkpoint from the database
right now and compares: if it doesn't match what was anchored,
that row (or an earlier one) was rewritten after anchoring — no
matter how correctly the chain was recomputed forward. It also
re-runs the ordinary internal-consistency check for everything
AFTER the checkpoint, since a break there is a different failure
(tampering since the last anchor) that comparing one hash value
can't see.

A privileged rewrite of a row anchored less than one anchoring
interval ago is undetectable until the daemon's next anchor
publishes a checkpoint past it — that window is inherent to
periodic anchoring, not a bug in this check.

With --anchor-bucket (or CONTAINARIUM_AUDIT_ANCHOR_BUCKET, the same
variable the daemon reads) the check also uses the daemon's copy of
the anchors in a GCS bucket, which a host-root attacker cannot
delete or overwrite while the bucket's retention policy is locked.
Every bucket checkpoint is compared with the database and with the
local file. A checkpoint above the database's highest row id, one
whose content does not match its name, one the local file lacks, or
one whose root differs is a failure (non-zero exit). Checkpoints
only the local file holds are still compared with the database
(a mismatch is a failure) and warned about, since the bucket does
not vouch for them. A bucket without a locked retention policy is a
warning (exit 0).

With --anchor-tsa-trust-bundle (or
CONTAINARIUM_AUDIT_ANCHOR_TSA_TRUST_BUNDLE) the check also uses the
RFC 3161 timestamp tokens the daemon stored next to the anchor file
(<anchor-path>.tsa/), which a timestamp authority signed over each
anchor record. A token that does not verify, is not signed by a TSA
chaining to the bundle at the token's time, covers a different record
than the local file holds (the line was edited), or has no local
record is a failure (non-zero exit). Every local checkpoint is also
compared with the database. A checkpoint with no token is a warning
(exit 0). The check is offline: it needs the bundle, not the TSA.

With neither a bucket nor a trust bundle configured, behaviour is
unchanged.`,
	Example: `  # Default anchor path (same as the daemon's default)
  containarium audit verify-anchor

  # Non-default anchor location
  containarium audit verify-anchor --anchor-path /mnt/forensics/audit-anchors.jsonl

  # Also check against the daemon's external anchor bucket
  containarium audit verify-anchor --anchor-bucket gs://<bucket>/<prefix>

  # Also check the RFC 3161 timestamp tokens against the TSA's root
  containarium audit verify-anchor --anchor-tsa-trust-bundle /etc/containarium/tsa-roots.pem`,
	RunE: runAuditVerifyAnchor,
}

func init() {
	rootCmd.AddCommand(auditCmd)
	auditCmd.AddCommand(auditQueryCmd)
	auditCmd.AddCommand(auditVerifyCmd)
	auditCmd.AddCommand(auditVerifyAnchorCmd)

	auditVerifyAnchorCmd.Flags().StringVar(&auditAnchorPath, "anchor-path", "",
		"Path to the anchor file (default: $CONTAINARIUM_AUDIT_ANCHOR_PATH, or the daemon's own default)")
	auditVerifyAnchorCmd.Flags().StringVar(&auditAnchorBucket, "anchor-bucket", "",
		"Also check against the anchors in this bucket, gs://<bucket>[/<prefix>] (default: $CONTAINARIUM_AUDIT_ANCHOR_BUCKET; unset checks the local file only)")
	auditVerifyAnchorCmd.Flags().StringVar(&auditAnchorTSATrustBundle, "anchor-tsa-trust-bundle", "",
		"Also check the RFC 3161 timestamp tokens in <anchor-path>.tsa/ against this PEM bundle of TSA root certificates (default: $CONTAINARIUM_AUDIT_ANCHOR_TSA_TRUST_BUNDLE; unset skips the token check)")

	auditQueryCmd.Flags().StringVar(&auditQueryUsername, "username", "", "Filter by username (exact match)")
	auditQueryCmd.Flags().StringVar(&auditQueryAction, "action", "", "Filter by action (exact match, e.g. create_container)")
	auditQueryCmd.Flags().StringVar(&auditQueryResource, "resource-type", "", "Filter by resource type (e.g. container, secret, api)")
	auditQueryCmd.Flags().StringVar(&auditQueryRunID, "run-id", "", "Filter by run id (exact match, e.g. an agent skill run)")
	auditQueryCmd.Flags().StringVar(&auditQueryFrom, "from", "", "Start of time range (RFC3339)")
	auditQueryCmd.Flags().StringVar(&auditQueryTo, "to", "", "End of time range (RFC3339)")
	auditQueryCmd.Flags().IntVar(&auditQueryLimit, "limit", 50, "Max rows to return (server caps at 1000)")
	auditQueryCmd.Flags().BoolVar(&auditQueryRaw, "raw", false, "Tab-separated output for grep/awk piping")

	auditVerifyCmd.Flags().Int64Var(&auditVerifyFromID, "from-id", 0, "Verify forward from this row ID (0 = chain start)")
	auditVerifyCmd.Flags().IntVar(&auditVerifyBatch, "batch", 1000, "Rows fetched per pass (memory-bound)")
}

