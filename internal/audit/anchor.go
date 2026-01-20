package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// #1706 — anchoring the hash chain's root outside the database.
//
// hash_chain.go's own doc comment already named the gap: "Append-only
// forensics (push the chain root to an external sink periodically) detects
// deletions on top of this; that's tracked separately." This is that.
//
// The chain (hash_chain.go) is tamper-evident against an application-level
// attacker: editing a row breaks VerifyChain from that point forward. It is
// SILENT against a privileged one — an operator with Postgres write access
// can rewrite a row and recompute every hash after it, and internal
// consistency alone can't tell the difference between "untouched" and
// "tampered, then correctly recomputed." The only thing that can tell the
// difference is a copy of an old hash value held somewhere the rewriter
// doesn't control. That's what RootSink is for.
//
// Design choice: the default sink (FileSink) is a local append-only file,
// not an object-store upload or a co-signed timestamp (the issue's other
// two suggested shapes). It satisfies the THREAT MODEL AS STATED — "an
// operator with write access to Postgres" — since a Postgres credential
// grants no access to the daemon host's filesystem. It does not defend
// against a full host-root attacker (who could edit both); that is a
// materially larger threat model than #1706 asks for, and the RootSink
// interface is the seam a stronger backend (object-store-with-versioning,
// a remote co-signed timestamp service) would slot into without changing
// anything else — AnchorManager and VerifyChainAgainstAnchor talk to the
// interface, not the file.

// RootSink durably records the hash chain's current tip. Implementations
// MUST reject a call whose checkpoint was already published with a
// DIFFERENT root — accepting one would let a second, conflicting write
// silently replace the first, defeating the entire point of anchoring.
// Republishing the SAME (checkpoint, root) pair is a safe no-op.
type RootSink interface {
	// PublishRoot records that as of audit-log row id `checkpoint`, the
	// chain's row_hash was `root`.
	PublishRoot(ctx context.Context, checkpoint int64, root string) error
	// LastPublished returns the most recently published (checkpoint,
	// root), or ok=false if nothing has ever been published.
	LastPublished(ctx context.Context) (checkpoint int64, root string, ok bool, err error)
}

// ErrAnchorConflict is returned by PublishRoot when checkpoint was already
// published with a different root than the one now being published.
var ErrAnchorConflict = errors.New("audit: checkpoint already anchored with a different root")

// anchorRecord is FileSink's on-disk line shape: one JSON object per line,
// append-only. AnchoredAt is informational only (not part of what's
// verified) — it lets an operator eyeball how stale the last anchor is
// without decoding a hash.
type anchorRecord struct {
	Checkpoint int64  `json:"checkpoint"`
	Root       string `json:"root"`
	AnchoredAt string `json:"anchored_at"`
}

// FileSink is the default RootSink: an append-only local file, opened
// O_APPEND for every write so a partial write from a crash can only ever
// truncate the LAST line, never corrupt an earlier one. Read side skips a
// trailing line that doesn't parse, rather than treating it as evidence the
// whole file is compromised (see lastPublishedLocked).
