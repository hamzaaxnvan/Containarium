package incus

import (
	"fmt"
	"os/exec"
	"strings"
)

// MigrationRunner is the small surface the migration orchestrator
// needs from incus. It exists as an interface so tests can swap in a
// fake that doesn't actually shell out — without it, every
// MoveContainer test would need a real Incus + a real remote daemon
// to copy to. The orchestration logic (snapshot ordering, delta
// iteration, cutover sequencing) is the interesting thing to test;
// the actual `incus copy` shell-out is well-trodden and not worth
// re-verifying in unit tests.
type MigrationRunner interface {
	// Snapshot creates a snapshot of the named container.
	// Idempotent on retry: if the snapshot exists, returns nil.
	Snapshot(container, snapshot string) error

	// DeleteSnapshot removes a previously-created snapshot. Used at
	// the end of a successful migration to clean up the sync<N> trail
	// of snapshots we left during pre-copy. No-op if the snapshot
	// doesn't exist.
	DeleteSnapshot(container, snapshot string) error

	// CopyInitial does the first full transfer of `container` with
	// snapshot `fromSnap` to `targetRemote`. `targetRemote` is the
	// name the source daemon's incusd uses for the destination — set
	// up once via `incus remote add` on the source. The destination
	// container takes the same name.
	//
	// storagePool, when non-empty, pins the destination pool the copy
	// lands in (`--storage`). Empty keeps Incus's own choice, which is the
	// destination's default pool — correct for an unencrypted container and
	// wrong for an encrypted one, whose data must land inside its tenant's
	// encryptionroot (#1203).
	//
	// Equivalent to: `incus copy <container>/<fromSnap> <remote>:<container> --instance-only [--storage <pool>]`
	CopyInitial(container, fromSnap, targetRemote, storagePool string) error

	// CopyRefresh syncs the delta from the source's previous-state
	// snapshot to `targetRemote`. Equivalent to:
	//   `incus copy <container>/<fromSnap> <remote>:<container> --refresh`
	// With ZFS/btrfs storage on both ends, this is a fast incremental
	// (zfs-send-style); with dir-pool storage it falls back to file
	// rsync, which works but isn't sub-second.
	CopyRefresh(container, fromSnap, targetRemote, storagePool string) error

	// Stop stops a container. Used at the very start of cutover; any
	// final state changes after this point are caught by the post-stop
	// snapshot+refresh.
	Stop(container string) error

	// Start starts a (possibly migrated) container, returning when
	// it's running. The source-side orchestrator uses this to undo a
	// pre-cutover Stop on failure; the destination-side adopter uses
	// it after AdoptMigratedContainer ensures host-user state is set
	// up.
	Start(container string) error

	// HasRemote reports whether the source's incusd knows about
	// `remoteName` as a destination it can copy to. The migration
	// fails fast if not — the operator needs to `incus remote add`
	// first. Auto-bootstrap of remotes is a separate follow-up.
	HasRemote(remoteName string) (bool, error)
}

// ExecRunner is the production MigrationRunner. It shells out to the
// `incus` CLI on the host. Lightweight and matches how the rest of
// this package shells out to `zfs` / `incus` for storage and exec
// operations — keeps the operational mental model identical.
type ExecRunner struct {
	// Path to the incus binary. Empty means look on $PATH.
	IncusPath string
}

func (e *ExecRunner) incus(args ...string) error {
	bin := e.IncusPath
	if bin == "" {
		bin = "incus"
	}
	cmd := exec.Command(bin, args...) // #nosec G204 -- args are validated by callers
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("incus %s: %w\nOutput: %s", strings.Join(args, " "), err, string(out))
	}
	return nil
}

// Snapshot — `incus snapshot create <container> <snapshot>` (#1392).
//
// `incus snapshot` is a command GROUP in Incus 6.x, not a verb. This used to
// call `incus snapshot <container> <snapshot>`, which that version rejects
// with `unknown command "<container>" for "incus snapshot"` — so MoveContainer
// failed on its very first call, before any copy. No test caught it because
// every move test substitutes a fake MigrationRunner and the real ExecRunner
// is wired only in production; the migration logic was covered, the CLI
// surface under it was not covered at all.
//
// If the snapshot already exists (rerun after a partial failure) the CLI errors
// with "already exists" — swallowed to keep retry idempotent.
func (e *ExecRunner) Snapshot(container, snapshot string) error {
	err := e.incus("snapshot", "create", container, snapshot)
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// DeleteSnapshot — `incus snapshot delete <container> <snapshot>` (#1392).
//
// This used to call `incus delete <container>/<snapshot>`, which Incus 6.x
