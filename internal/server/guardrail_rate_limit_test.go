package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/guardrailrate"
	"github.com/footprintai/containarium/internal/guardrailstage"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Rate limits on the guardrail APIs (#2404). Every limiter here runs on a
// frozen clock: nothing refills during a test, and nothing sleeps.

func frozenLimiter(name string, lim guardrailrate.Limit) *guardrailrate.Limiter {
	at := time.Unix(1_700_000_000, 0)
	return guardrailrate.New(name, lim,
		guardrailrate.WithClock(func() time.Time { return at }),
		guardrailrate.WithLogf(func(string, ...any) {}))
}

// stagingListing is every path under the staging root with its size and
// mtime, so a test can assert a refused deploy left it untouched.
func stagingListing(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%s %v %d", fi.ModTime(), fi.Mode(), fi.Size())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Acceptance criterion 1: over the gated-deploy limit, the deploy is
// RESOURCE_EXHAUSTED before any snapshot is taken; the fake backend creates
// nothing and the staging directory is untouched.
func TestDeployGate_RateLimitedBeforeAnySnapshot(t *testing.T) {
	f := newGateFixture(t)
	f.srv.SetGuardrailDeployRateLimit(frozenLimiter("gated deploy", guardrailrate.Limit{PerMinute: 6, Burst: 1}))
	snapshots := 0
	f.srv.guardrailSnapshot = func(a *guardrailstage.Area, ref, parent string) (*guardrailstage.Snapshot, error) {
		snapshots++
		return a.Snapshot(ref, parent)
	}

	if _, err := f.deploy(t); err != nil {
		t.Fatalf("first deploy (within burst): %v", err)
	}
	if snapshots != 1 || len(f.boxes.created) != 1 {
		t.Fatalf("first deploy: %d snapshot(s), created %v; want 1 and one box", snapshots, f.boxes.created)
	}
	before := stagingListing(t, f.srv.guardrailStagingRoot)
	events := len(f.boxes.events)

	_, err := f.deploy(t)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second deploy = %v, want ResourceExhausted", err)
