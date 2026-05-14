package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeSeedWiper records the wipe exec runlease.End issues. The production
// Wiper is *container.Manager, whose Exec type-asserts its backend to the
// concrete *incus.Client, so the runlease.Wiper interface is the only seam a
// unit test can reach the wipe through.
type fakeSeedWiper struct {
	mu    sync.Mutex
	boxes []string
	cmds  [][]string
}

func (w *fakeSeedWiper) Exec(box string, cmd []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.boxes = append(w.boxes, box)
	w.cmds = append(w.cmds, cmd)
	return nil
}

func (w *fakeSeedWiper) calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.cmds)
}

// ctxAwareRevoker is fakeRevocationStore with the one behavior the real
// Postgres store has and the fake does not: it fails a revoke whose context is
// already done. Without it a cancelled-caller test passes even when the lease
// ends on the RPC's own (cancelled) context, because the fake ignores ctx.
type ctxAwareRevoker struct{ *fakeRevocationStore }

func (r ctxAwareRevoker) Revoke(ctx context.Context, jti string, expiresAt time.Time, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.fakeRevocationStore.Revoke(ctx, jti, expiresAt, reason)
}

// listRevocations reads the fake store's contents through its own locking.
func listRevocations(t *testing.T, store *fakeRevocationStore) []auth.Revocation {
	t.Helper()
	out, err := store.List(context.Background(), auth.ListRevocationsParams{})
	if err != nil {
		t.Fatalf("List revocations: %v", err)
	}
	return out
}

// fakeAuditLogger records the audit entries this server writes. *audit.Store
// needs a live Postgres pool, so the auditLogger interface is the only way to
// assert on what actually lands in audit_logs — the action, the resource type,
// and the run_id column this feature is the first writer of.
//
// Log refuses an already-done context, the way a real pool does. That is what
// makes the detached-write assertions real: without it a cancelled caller's row
// is still recorded and the test passes even with context.WithoutCancel
// removed — the same trap ctxAwareRevoker exists for.
//
// blockUntilDone makes Log wait for its context instead of returning, which is
// how the bounded-write test stands in for a wedged Postgres holding the
// audit chain's global advisory lock.
type fakeAuditLogger struct {
	mu             sync.Mutex
	entries        []audit.AuditEntry
	err            error
	blockUntilDone bool
}

func (f *fakeAuditLogger) Log(ctx context.Context, e *audit.AuditEntry) error {
	if f.blockUntilDone {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, *e)
	return f.err
}

func (f *fakeAuditLogger) byAction(action string) []audit.AuditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []audit.AuditEntry
	for _, e := range f.entries {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// testLease is a two-credential lease shaped like the one provisionSkillBox
// hands back for a gateway-mode run.
func testLease(runID string) runlease.Lease {
	exp := time.Now().Add(agentTokenTTL)
	return runlease.Lease{
		RunID:   runID,
		Box:     "agent-hello-agent-container",
		SeedDir: seedDirFor(runID),
		Credentials: []runlease.Credential{
			{Kind: runlease.KindPlatformJWT, JTI: "jti-platform", ExpiresAt: exp},
			{Kind: runlease.KindGatewayToken, JTI: "jti-gateway", ExpiresAt: exp},
		},
	}
}

func TestResolveRunID_Table(t *testing.T) {
	maxID := strings.Repeat("a", 128)
	cases := []struct {
		name     string
		in       string
		wantEcho bool // echoes the input verbatim
		wantCode codes.Code
	}{
		{name: "empty generates a uuid", in: ""},
		{name: "simple id echoes", in: "run-1", wantEcho: true},
		{name: "every allowed class echoes", in: "Run_9.a-Z", wantEcho: true},
		{name: "128 chars echoes", in: maxID, wantEcho: true},
		{name: "129 chars rejected", in: maxID + "a", wantCode: codes.InvalidArgument},
		{name: "slash rejected", in: "run/1", wantCode: codes.InvalidArgument},
		{name: "shell metachars rejected", in: "run;rm -rf /", wantCode: codes.InvalidArgument},
		{name: "space rejected", in: "run 1", wantCode: codes.InvalidArgument},
		{name: "newline rejected", in: "run\n1", wantCode: codes.InvalidArgument},
		// #1860: a run id becomes a single path segment under
		// /etc/containarium/agent/runs and /workspace/runs — "." and ".."
		// both match runIDPattern's character class but must never resolve
		// to the roots themselves.
		{name: "dot rejected", in: ".", wantCode: codes.InvalidArgument},
		{name: "dot-dot rejected", in: "..", wantCode: codes.InvalidArgument},
		{name: "dot as a real id segment still echoes", in: "run.1", wantEcho: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRunID(tc.in)
			if tc.wantCode != codes.OK {
				if status.Code(err) != tc.wantCode {
					t.Fatalf("resolveRunID(%q) error = %v (code %v), want code %v", tc.in, err, status.Code(err), tc.wantCode)
				}
				if got != "" {
					t.Errorf("rejected input must yield no run id, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRunID(%q): unexpected error %v", tc.in, err)
			}
			if tc.wantEcho {
				if got != tc.in {
					t.Errorf("resolveRunID(%q) = %q, want the input echoed", tc.in, got)
				}
				return
			}
			if _, perr := uuid.Parse(got); perr != nil {
				t.Errorf("resolveRunID(%q) = %q, want a generated uuid: %v", tc.in, got, perr)
			}
		})
	}
}

// TestEndRunLease_UsesDetachedContext is the caller-cancel exit path: the RPC
// context is already dead when the lease ends, and both credentials must still
// be revoked and the seed files still wiped. Without context.WithoutCancel the
// revokes would fail with context.Canceled.
func TestEndRunLease_UsesDetachedContext(t *testing.T) {
	store := newFakeRevocationStore()
	s := &AgentSkillServer{}
	s.SetRevocationStore(ctxAwareRevoker{store})
	w := &fakeSeedWiper{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller is gone before the lease ends

	s.endRunLease(ctx, testLease("run-detached"), w, runExitReason)

	for _, jti := range []string{"jti-platform", "jti-gateway"} {
		revoked, err := store.IsRevoked(context.Background(), jti)
		if err != nil {
			t.Fatalf("IsRevoked(%s): %v", jti, err)
		}
		if !revoked {
			t.Errorf("%s was not revoked — the lease did not end under a detached context", jti)
		}
	}
	// Reasons come back through List rather than store.revoked[jti]: the map is
	// mutex-guarded and reading it bare would be a race the moment anything in
	// this package revokes concurrently.
	for _, r := range listRevocations(t, store) {
		if r.Reason != runExitReason {
			t.Errorf("%s revoked with reason %q, want %q", r.JTI, r.Reason, runExitReason)
		}
	}
	// #1860: endRunLease now runs a second Exec (rm -rf the seed dir) after
	// the file wipe; wipe stays cmds[0], the dirs removal is cmds[1].
	if w.calls() != 2 {
		t.Fatalf("exec calls = %d, want 2 (wipe files, then remove dirs)", w.calls())
	}
	seedDir := seedDirFor("run-detached")
	wantCmd := []string{"rm", "-f", seedDir + "/token", seedDir + "/gateway.env"}
	if strings.Join(w.cmds[0], " ") != strings.Join(wantCmd, " ") {
		t.Errorf("wipe cmd = %v, want %v", w.cmds[0], wantCmd)
	}
	wantRemoveCmd := []string{"rm", "-rf", seedDir}
	if strings.Join(w.cmds[1], " ") != strings.Join(wantRemoveCmd, " ") {
		t.Errorf("remove dirs cmd = %v, want %v", w.cmds[1], wantRemoveCmd)
	}
	if w.boxes[0] != "agent-hello-agent-container" {
		t.Errorf("wipe box = %q, want the lease's box", w.boxes[0])
	}
}

// TestEndRunLease_UnregistersFromRunRegistry pins the #1922 wiring: a
// run's entry in the shared run registry is removed exactly when its
// lease ends, so a tracker write RPC can no longer resolve or claim
// liveness for a run that has finished.
func TestEndRunLease_UnregistersFromRunRegistry(t *testing.T) {
	registry := runlease.NewRegistry()
	registry.Register("run-registry-cleanup", runlease.Info{SkillID: "hello-agent", Model: "sonnet"})

	s := &AgentSkillServer{}
	s.SetRunRegistry(registry)
	w := &fakeSeedWiper{}

	if !registry.Live("run-registry-cleanup") {
		t.Fatal("setup: run should be live before endRunLease")
	}
	s.endRunLease(context.Background(), testLease("run-registry-cleanup"), w, runExitReason)

	if registry.Live("run-registry-cleanup") {
		t.Error("run still shows as live in the registry after endRunLease")
	}
}

// TestEndRunLease_NilRunRegistry_DoesNotPanic covers a daemon where
// dual_server.go's SetRunRegistry wiring hasn't run (e.g. tests
// constructing AgentSkillServer directly) — the nil-guard at the call
// site must make this a no-op, not a crash.
func TestEndRunLease_NilRunRegistry_DoesNotPanic(t *testing.T) {
	s := &AgentSkillServer{} // runs field left nil
	w := &fakeSeedWiper{}
	s.endRunLease(context.Background(), testLease("run-no-registry"), w, runExitReason)
}

// TestEndRunLease_LogsWhenUnrevoked covers the no-Postgres daemon: the run
// still completes and the files are still wiped, but the operator gets exactly
// one line naming the run whose credentials will now live to their expiry.
func TestEndRunLease_LogsWhenUnrevoked(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s := &AgentSkillServer{} // no revocation store wired
	w := &fakeSeedWiper{}
	s.endRunLease(context.Background(), testLease("run-nostore"), w, runExitReason)

	out := buf.String()
	if !strings.Contains(out, "run-nostore") {
		t.Errorf("log must name the run, got %q", out)
	}
	// Counted by substring, not "one line in the buffer": log output is
	// process-global, so a stray line from another test's background goroutine
	// would otherwise turn this into an unreadable CI flake instead of a
	// failure. One line per run is still exactly what is asserted.
	if n := strings.Count(out, "could not be revoked"); n != 1 {
		t.Errorf("want exactly one unrevoked-credentials line per run, got %d:\n%s", n, out)
	}
	// #1860: wipe files, then remove dirs — both still run without a store.
	if w.calls() != 2 {
		t.Errorf("files must still be wiped and dirs removed without a store; exec calls = %d", w.calls())
	}
}

// TestRunLeaseAuditPayloads pins the Detail JSON of both audit rows: it is
// marshalled from named Go structs (never map[string]string) and carries the
// run id plus every minted jti, which is what makes "revoke everything this
// run was given" answerable from the audit trail alone.
func TestRunLeaseAuditPayloads(t *testing.T) {
	lease := testLease("run-audit")

	var issue runLeaseIssueDetail
	if err := json.Unmarshal([]byte(runLeaseIssuePayload(lease, pb.AgentEngine_AGENT_ENGINE_CLAUDE)), &issue); err != nil {
		t.Fatalf("unmarshal issue payload: %v", err)
	}
	if issue.RunID != "run-audit" {
		t.Errorf("issue run_id = %q, want run-audit", issue.RunID)
	}
	if issue.Box != lease.Box {
		t.Errorf("issue box = %q, want %q", issue.Box, lease.Box)
	}
	// #2222: the run's resolved engine rides the same audit row, as its
	// EnvValue name — what an auditor reading the daemon's own
	// CONTAINARIUM_AGENT_ENGINE export would see.
	if issue.Engine != "claude" {
		t.Errorf("issue engine = %q, want claude", issue.Engine)
	}
	if len(issue.Credentials) != 2 {
		t.Fatalf("issue credentials = %d, want both minted credentials", len(issue.Credentials))
	}
	for i, want := range []runLeaseCredential{
		{Kind: string(runlease.KindPlatformJWT), JTI: "jti-platform"},
		{Kind: string(runlease.KindGatewayToken), JTI: "jti-gateway"},
	} {
		if issue.Credentials[i].Kind != want.Kind || issue.Credentials[i].JTI != want.JTI {
			t.Errorf("issue credential %d = %+v, want kind %q jti %q", i, issue.Credentials[i], want.Kind, want.JTI)
		}
