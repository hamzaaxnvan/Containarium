// Package guardrailstage is the daemon-side staging area a gated recipe
// deploy reads its dataset from (#2368;
// docs/architecture/guardrail-inbound-and-server-policy.md, "The gate").
//
// A DeployRecipeRequest names its dataset by staging_ref: a directory under
// one operator-configured staging root on the daemon host. There is no
// platform path that puts a dataset there yet (the `ship` verb is a
// documented gap), so an operator or an out-of-band copy stages it. This
// package does two things with a ref:
//
//   - refuses any ref that does not name a real directory inside the root:
//     absolute paths, "..", non-clean forms, and any symlink on the ref's own
//     path, even one that points back inside the root (another tenant's
//     directory is inside the root too). The ref is opened one component at
//     a time and each opened directory must be the inode that was checked,
//     so swapping a component for a symlink mid-open is refused, not
//     followed;
//   - copies the staged regular files into a fresh private directory (the
//     snapshot). The gate verifies the snapshot and delivers the snapshot, so
//     the bytes verified and the bytes delivered are the same bytes, however
//     the staging area changes in between.
//
// Opens go through os.Root, so no read can leave the staging root even if
// the tree is changed while it is being copied. Symlinks inside the dataset
// are skipped, exactly as guardrail.SubjectDigest skips them. Files are
// opened non-blocking and must still be regular files once open, so a file
// swapped for a FIFO is skipped instead of hanging the snapshot.
//
// Hard links are not symlinks: a hard link inside the dataset to a file
// outside the root is copied. That needs no special handling, because
// whoever stages can only hard-link a file they can already read (and, with
// fs.protected_hardlinks, own or write), so it grants them nothing.
//
// Errors: ErrBadRef and ErrBadParent name only the caller's ref. Any other
// error is internal (it may carry daemon-side detail); callers such as the
// DeployRecipe gate must log it and return a generic message, never echo it.
package guardrailstage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrNoStagingRoot means the daemon has no staging root configured, so a
// gated deploy has nowhere to read a dataset from and must be refused.
var ErrNoStagingRoot = errors.New("guardrail staging root is not configured")

// ErrBadRef wraps every reason a staging_ref is refused.
var ErrBadRef = errors.New("staging_ref must name a directory inside the guardrail staging root")

// ErrBadParent means the snapshot's parent directory is inside the staging
// root, where whoever stages could swap the snapshot after verification.
var ErrBadParent = errors.New("guardrail snapshot parent must be a daemon-private directory outside the staging root")

// ErrStagingRootUnavailable means the configured root could not be opened
// (removed or unreadable since New). It never embeds the host path.
var ErrStagingRootUnavailable = errors.New("guardrail staging root is unavailable")

// Area is a configured staging root.
type Area struct {
	root string
}

// New checks root is an absolute path to an existing directory.
func New(root string) (*Area, error) {
	if root == "" {
		return nil, ErrNoStagingRoot
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("guardrail staging root %q must be an absolute path", root)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("guardrail staging root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("guardrail staging root %q is not a directory", root)
	}
	return &Area{root: filepath.Clean(root)}, nil
}

// Snapshot is a private copy of one staged dataset.
type Snapshot struct {
	// Dir holds the copied files; mode 0700, owned by the daemon.
	Dir string
}

// Remove deletes the snapshot.
func (s *Snapshot) Remove() error { return os.RemoveAll(s.Dir) }

// testHook, when set by a test, runs at the named point with the name about
// to be opened. It lets a test change the tree at exactly the instant a
// race would, deterministically. nil in production.
var testHook func(point, name string)

const (
	hookBeforeOpenDir  = "before-open-dir"
	hookBeforeOpenFile = "before-open-file"
)

func hook(point, name string) {
	if testHook != nil {
