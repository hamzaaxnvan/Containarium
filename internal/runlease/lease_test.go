package runlease

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// callRecorder records the order in which fake steps ran, shared between a
// test's fakeRevoker and fakeWiper so tests can assert call order across
// both.
type callRecorder struct {
	mu    sync.Mutex
	order []string
}

func (c *callRecorder) record(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, s)
}

func (c *callRecorder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// fakeRevoker is a Revoker that records every call, can be told to return a
// per-jti error, and can be told to delay a per-jti response past the
// caller's timeout (while still honoring ctx.Done() so it doesn't leak).
type fakeRevoker struct {
	rec      *callRecorder
	mu       sync.Mutex
	calls    []string
	errFor   map[string]error
	delayFor map[string]time.Duration
}

func newFakeRevoker() *fakeRevoker {
	return &fakeRevoker{rec: &callRecorder{}}
}

func newFakeRevokerWithRecorder(rec *callRecorder) *fakeRevoker {
	return &fakeRevoker{rec: rec}
}

func (f *fakeRevoker) Revoke(ctx context.Context, jti string, expiresAt time.Time, reason string) error {
	f.mu.Lock()
	f.calls = append(f.calls, jti)
	delay := f.delayFor[jti]
	err := f.errFor[jti]
	f.mu.Unlock()

	if f.rec != nil {
		f.rec.record("revoke:" + jti)
	}

	// A real store makes its call with ctx, so an already-expired or
	// already-cancelled context fails the call exactly like a real one
	// would.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeRevoker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeWiper is a Wiper that records every call, can be told to return an
// injected error, and can be told to block for a fixed delay before
// returning (simulating a slow rm) — Exec has no context to honor, so the
// delay is a plain time.Sleep.
type fakeWiper struct {
	rec   *callRecorder
	mu    sync.Mutex
	got   []wipeCall
	err   error
	delay time.Duration
}

type wipeCall struct {
	container string
	cmd       []string
}

func newFakeWiper() *fakeWiper {
	return &fakeWiper{rec: &callRecorder{}}
}

func newFakeWiperWithRecorder(rec *callRecorder) *fakeWiper {
	return &fakeWiper{rec: rec}
}

func (f *fakeWiper) Exec(container string, cmd []string) error {
	f.mu.Lock()
	cp := make([]string, len(cmd))
	copy(cp, cmd)
	f.got = append(f.got, wipeCall{container: container, cmd: cp})
	err := f.err
	delay := f.delay
	f.mu.Unlock()

	// Record the attempt before blocking, so a caller racing this Exec
	// against a timeout still observes that the wipe was attempted even
	// if it never observes this call return.
	if delay > 0 {
		time.Sleep(delay)
	}

	if f.rec != nil {
		// Distinguish the two Wiper.Exec callers by the shape of their argv
		// (cmd[1] is "-f" for wipeSeed's file wipe, "-rf" for removeDirs'
		// directory removal) so an order-assertion can tell them apart.
		label := "wipe"
		if len(cmd) > 1 {
			switch cmd[1] {
			case "-f":
				label = "wipe-files"
			case "-rf":
				label = "wipe-dirs"
			}
		}
		f.rec.record(label)
	}
	return err
}

func (f *fakeWiper) calls() []wipeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wipeCall, len(f.got))
	copy(out, f.got)
	return out
}

func testLease(box, seedDir string, creds ...Credential) Lease {
	return Lease{
		RunID:       "run-1",
		Box:         box,
		SeedDir:     seedDir,
		Credentials: creds,
	}
}

func cred(kind Kind, jti string) Credential {
	return Credential{Kind: kind, JTI: jti, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestEnd_RevokesEveryCredentialThenWipes(t *testing.T) {
	rec := &callRecorder{}
	rev := newFakeRevokerWithRecorder(rec)
	w := newFakeWiperWithRecorder(rec)

	lease := testLease("box-1", "/etc/containarium/agent",
		cred(KindPlatformJWT, "jti-1"),
		cred(KindGatewayToken, "jti-2"),
	)

	out := End(context.Background(), lease, rev, w, "run_exit")

	// #1860: a third step, removing the seed dir + workspace, runs after the
	// existing revoke-then-wipe-files order.
	want := []string{"revoke:jti-1", "revoke:jti-2", "wipe-files", "wipe-dirs"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if len(out.Revoked) != 2 || len(out.Unrevoked) != 0 || !out.Wiped || !out.DirsRemoved || len(out.Errs) != 0 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
}

func TestEnd_Table(t *testing.T) {
	// wantExecCalls counts BOTH Wiper.Exec callers (wipeSeed's file wipe AND
	// removeDirs' directory removal, #1860) — every case below leaves
	// Workspace empty (the zero value from testLease), so removeDirs' argv
	// never carries a second path; TestEnd_RemovesDirsWithWorkspace covers
	// that separately. wipeErr/wipeDelay are injected on the shared fake and
	// so apply to BOTH Exec calls identically — that is why "wipe errors" and
	// "wipe times out" below now expect TWO errors, not one: each step fails
	// independently, and both are always attempted.
	tests := []struct {
		name            string
		seedDir         string
		creds           []Credential
		nilRevoker      bool
		errFor          map[string]error
		delayFor        map[string]time.Duration
		wipeErr         error
		wipeDelay       time.Duration
		wantRevoked     []string
		wantUnrevoked   []string
		wantWiped       bool
		wantDirsRemoved bool
		wantErrs        int
		wantErrSubstr   string // when non-empty, at least one Errs entry must contain it
		wantRevokeCalls int
		wantExecCalls   int
		wantElapsedMin  time.Duration // zero skips the check
		wantElapsedMax  time.Duration // zero skips the check
	}{
		{
			name:            "all ok",
			creds:           []Credential{cred(KindPlatformJWT, "jti-1"), cred(KindGatewayToken, "jti-2")},
			wantRevoked:     []string{"jti-1", "jti-2"},
			wantUnrevoked:   nil,
			wantWiped:       true,
			wantDirsRemoved: true,
			wantErrs:        0,
			wantRevokeCalls: 2,
			wantExecCalls:   2,
		},
		{
