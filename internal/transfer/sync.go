package transfer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// SyncOptions extends Options with sync-specific knobs.
type SyncOptions struct {
	Options

	// Delete: when true, remove files on the remote that no longer
	// exist locally. Off by default — additive sync is safer for a v1.
	Delete bool

	// Excludes: substring patterns to skip during the local walk. Sensible
	// defaults for common build/cache directories are applied if empty.
	Excludes []string
}

// DefaultSyncExcludes is the noise filter applied when SyncOptions.Excludes
// is empty. Substring match, not glob.
//
// .env* is excluded by default because env files are per-environment
// secrets — clobbering a container's .env with a laptop's .env is a
// classic footgun (surfaced 2026-05-12 via agent feedback). If a caller
// genuinely wants to ship env files they can pass Excludes explicitly
// to override.
var DefaultSyncExcludes = []string{
	"node_modules/",
	".terraform/",
	"__pycache__/",
	".pytest_cache/",
	".venv/",
	"venv/",
	".DS_Store",
	".idea/",
	".vscode/",
	".env",
	".env.",
	".envrc",
}

// SyncResult summarizes what changed.
type SyncResult struct {
	Added    int
	Modified int
	Deleted  int
	Bytes    int64
}

// Sync mirrors LocalPath to RemotePath, shipping only files whose
// sha256+mode differs. Includes .git/ by design (so committed history,
// branches, stashes carry over). Symlinks and special files are skipped.
//
// On first call (empty remote), every file is shipped. On subsequent
// calls, only the delta.
func Sync(opt SyncOptions) (*SyncResult, error) {
	if err := opt.resolve(); err != nil {
		return nil, err
	}
	if len(opt.Excludes) == 0 {
		opt.Excludes = DefaultSyncExcludes
	}

	// 1. Build local manifest.
	if opt.Verbose {
		fmt.Fprintf(os.Stderr, "[sync] hashing %s ...\n", opt.LocalPath)
	}
	local, err := walkLocal(opt.LocalPath, opt.Excludes)
	if err != nil {
		return nil, fmt.Errorf("walk local: %w", err)
	}

	// 2. Read remote manifest. The remote script tolerates a missing
	// RemotePath by creating it.
	if opt.Verbose {
		fmt.Fprintf(os.Stderr, "[sync] reading remote manifest at %s ...\n", opt.RemotePath)
	}
	remote, err := readRemoteManifest(opt)
	if err != nil {
		return nil, fmt.Errorf("read remote manifest: %w", err)
	}

	// 3. Diff.
	d := local.diff(remote)

	// Empty diff (and not asked to --delete extras) → nothing to do.
