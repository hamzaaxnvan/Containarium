//go:build integration

// #2980: the purge bound is the LOWER of the local anchor file's and the
// external bucket's latest checkpoint (audit.SafeAnchor). The unit test
// TestSafeAnchor covers the function; these tests run a real purge through the
// sweeper with the anchor wired the way the daemon wires it, so choosing the
// higher of the two would delete rows an unvouched-for anchor says are safe.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/retention/pgstore/
package pgstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/retention/pgstore"
)

// anchorFromBothCopies wires the purge anchor like the daemon: SafeAnchor over
// a real local anchor file holding a real row hash at fileCP, and an external
// sink whose latest checkpoint is bucketCP with its real row hash.
func (e *chainEnv) anchorFromBothCopies(t *testing.T, fileCP, bucketCP int64) {
	t.Helper()
	file, err := audit.NewFileSink(filepath.Join(t.TempDir(), "anchors.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.PublishRoot(e.ctx, fileCP, mustHash(t, e, fileCP)); err != nil {
		t.Fatal(err)
	}
	external := fixed{bucketCP, mustHash(t, e, bucketCP)}
	e.source.SetAnchor(func(ctx context.Context) (pgstore.Anchor, bool, error) {
		a, ok, err := audit.SafeAnchor(ctx, file, external)
		return pgstore.Anchor{Checkpoint: a.Checkpoint, Root: a.Root}, ok, err
	})
