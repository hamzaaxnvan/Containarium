package zfscrypt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Snapshot operations under encryption (#1202).
//
// The design's resolved decision #3: snapshot CREATION is allowed while the
// key is unavailable, and the inspection-time read is what fails. Blocking
// creation on key custody would let a transient KMS outage silently suppress
// a backup window — losing the backup is worse than having one that cannot
// be read until custody recovers.
//
// That split rests on a claim about ZFS: snapshots of an encrypted dataset
// can be taken, listed and destroyed with the key unloaded, because those
// operate on metadata rather than on the encrypted contents. It is verified
// against a real pool in zfscrypt_integration_test.go rather than assumed.

// ErrKeyUnavailableForInspection reports that a snapshot exists but cannot
// be read because its dataset's key is not loaded.
//
// A distinct error because the caller's remedy is specific and the raw ZFS
// message is not: the snapshot is intact, nothing is lost, and the fix is to
// restore key custody and retry (#1202 AC2).
var ErrKeyUnavailableForInspection = fmt.Errorf("snapshot contents cannot be read: the dataset's encryption key is not loaded")

// validateSnapshotName rejects names that would change which object a
// command acts on. `@` in particular would turn one snapshot reference into
// another.
func validateSnapshotName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("snapshot name is required")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("invalid snapshot name %q: leading dash would be read as a flag", name)
	case strings.ContainsAny(name, " \t\n\r@/"):
		return fmt.Errorf("invalid snapshot name %q: must not contain whitespace, '@' or '/'", name)
	}
	return nil
}

// Snapshot creates <dataset>@<name> and returns the full snapshot name.
//
// Deliberately does NOT require the key. See the package-level note above:
// a backup that cannot be read yet beats no backup at all.
func (m *Manager) Snapshot(ctx context.Context, dataset, name string) (string, error) {
	if err := validateDataset(dataset); err != nil {
		return "", err
	}
	if err := validateSnapshotName(name); err != nil {
		return "", err
