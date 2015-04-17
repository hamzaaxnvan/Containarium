package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// queueFor queues a miner-signature finding from box, with no worker
// running. Every box uses the same subject, so all of one tenant's boxes
// share one pre-#2778 finding. This fixture exercises persisted legacy rows;
// new production subjects are tested through the real miner rule separately.
func queueFor(t *testing.T, fx *responderFixture, sink *ActingSink, box string) responseJob {
	t.Helper()
	if _, err := sink.Upsert(context.Background(), &threatdetect.Finding{Rule: ruleMinerSignature,
		Severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH, TenantID: "tenant-a", Container: box, Subject: "name:xmrig"}); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-fx.r.queue:
		if job.f.Container != box {
			t.Fatalf("queued job is for %q, want %q", job.f.Container, box)
		}
		return job
	default:
		t.Fatal("nothing queued")
		return responseJob{}
	}
}

func openAction(fx *responderFixture, box string) (*threatdetect.SecurityAction, error) {
	return fx.actions.Open(context.Background(), box)
}

// A store-side state change immediately before the worker's locked read
// must prevent both a kill and a policy action. The hook belongs to the fake
// store; production no longer needs a test-only pre-lock callback (#2778).
func TestQueuedResponse_StateChangeAtReadSkipsKill(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, fx *responderFixture, sink *ActingSink)
		land   func(t *testing.T, fx *responderFixture, job responseJob)
		reason string
	}{
		{
			name: "finding resolved",
			land: func(t *testing.T, fx *responderFixture, job responseJob) {
				if _, err := fx.findings.Resolve(context.Background(), job.f.ID); err != nil {
					t.Fatal(err)
				}
			},
			reason: "no longer open",
		},
		{
			name: "own container's action expired",
			setup: func(t *testing.T, fx *responderFixture, sink *ActingSink) {
				fx.r.runJob(context.Background(), queueFor(t, fx, sink, "box-a"))
			},
			land: func(t *testing.T, fx *responderFixture, _ responseJob) {
				row, err := fx.actions.Open(context.Background(), "box-a")
				if err != nil {
					t.Fatal(err)
				}
				if err := fx.actions.Release(context.Background(), row.ID, threatdetect.ReleasedByTTL, fx.clock.Add(2*time.Hour)); err != nil {
					t.Fatal(err)
				}
			},
			reason: "action on box-a released by ttl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fx, sink := queuedThrottle(t)
			if tc.setup != nil {
				tc.setup(t, fx, sink)
			}
			fx.setKill(t, "", true)
			killer := &fakeKiller{}
			fx.r.SetProcessKiller(killer)
			job := queueSignature(t, fx, sink, 4242)
			landed := 0
			fx.findings.beforeGet = func() {
				fx.findings.beforeGet = nil
				landed++
				tc.land(t, fx, job)
			}

			fx.r.runJob(ctx, job)

			if landed != 1 {
				t.Fatalf("seam ran %d time(s), want 1", landed)
			}
			if len(killer.calls) != 0 || len(auditDetails(fx.audit, "kill")) != 0 {
				t.Fatalf("killed %v after a %s landed before the locked re-check", killer.calls, tc.name)
			}
			if d := auditDetails(fx.audit, "response-skipped"); len(d) != 1 || !strings.Contains(d[0], tc.reason) {
				t.Fatalf("response-skipped audit = %v, want one containing %q", d, tc.reason)
			}
		})
	}
}

// flakyPolicyKV is a policy config store whose reads can be made to fail.
type flakyPolicyKV struct {
	mu   sync.Mutex
	m    map[string]string
	fail bool
}

func (k *flakyPolicyKV) Get(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail {
		return "", errors.New("config store unavailable")
	}
	v, ok := k.m[key]
	if !ok {
		return "", app.ErrDaemonConfigNotFound
	}
	return v, nil
}

func (k *flakyPolicyKV) Set(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

// TestProcessKilledRecorded_PolicyReadFailsAfterKill (#2766): the kill ran
// and was audited, then the policy store went away. The finding must still
// say the process was killed. Since #2768 the kill and the policy action
// share one policy read, so the store failing after the kill no longer
// skips the policy action: it applies the policy the kill was decided on.
func TestProcessKilledRecorded_PolicyReadFailsAfterKill(t *testing.T) {
	ctx := context.Background()
	fx := newResponderFixture(t, false)
	kv := &flakyPolicyKV{m: map[string]string{}}
	fx.policies = threatdetect.NewProcessPolicyStore(kv)
	fx.r.policies = fx.policies
	fx.setDefault(t, pb.ProcessAction_PROCESS_ACTION_THROTTLE, "10%", time.Hour)
	fx.setKill(t, "", true)
	fx.r.SetProcessKiller(killerFunc(func(string, uint32) error {
		kv.mu.Lock()
		kv.fail = true // the store goes away right after the kill
		kv.mu.Unlock()
		return nil
	}))

	out := fx.fireSignature(t, "box-a", 4242)

	if len(auditDetails(fx.audit, "kill")) != 1 {
		t.Fatalf("kill audits = %v, want one", auditDetails(fx.audit, "kill"))
	}
	if out == nil || out.Response == nil || !out.Response.ProcessKilled {
		t.Fatalf("returned finding = %+v, want process_killed=true", out)
	}
	f, _ := fx.findings.Get(ctx, out.ID)
	if f.Response == nil || !f.Response.ProcessKilled {
		t.Fatalf("stored response = %+v, want process_killed=true", f.Response)
	}
	if setCPUCalls(fx) != 1 {
		t.Fatalf("throttle applied %d times, want once, from the policy read the kill used", setCPUCalls(fx))
	}
}

// TestProcessKilledRecorded_SurvivesRelease (#2766): a kill recorded while
// another action stood on the box (killed_pid is not on that row) must not
// be wiped by the release that later rewrites the finding's response.
func TestProcessKilledRecorded_SurvivesRelease(t *testing.T) {
	ctx := context.Background()
	fx := newResponderFixture(t, false)
	fx.setDefault(t, pb.ProcessAction_PROCESS_ACTION_THROTTLE, "10%", time.Hour)
	fx.r.SetProcessKiller(&fakeKiller{})
	fx.fireSignature(t, "box-a", 100) // throttled, kill off: killed_pid 0
	fx.setKill(t, "", true)
	f := fx.fireSignature(t, "box-a", 101) // throttle stands, the kill is recorded
	if f == nil || f.Response == nil || !f.Response.ProcessKilled {
		t.Fatalf("after the kill: %+v, want process_killed=true", f)
	}

	released, _, err := fx.r.Release(ctx, "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if released == nil || released.Response == nil || !released.Response.ProcessKilled {
		t.Fatalf("after release: %+v, want process_killed still true", released)
	}
}

// TestRelease_SharedFindingHeldByAppliedAction (#2772 review 1): box-a and
// box-b are both throttled by one finding and both jobs already ran, so
// nothing is queued. Release(box-a) must still leave the finding open, since
// box-b's action is open on it (read from the store, not from the queue);
// Release(box-b) then resolves it as the last box.
func TestRelease_SharedFindingHeldByAppliedAction(t *testing.T) {
	ctx := context.Background()
	fx, sink := queuedThrottle(t)
	fx.r.runJob(ctx, queueFor(t, fx, sink, "box-a"))
	jobB := queueFor(t, fx, sink, "box-b")
	fx.r.runJob(ctx, jobB)

	f, kept, err := fx.r.Release(ctx, "box-a")
	if err != nil {
		t.Fatal(err)
	}
	if !kept || f == nil || f.State != threatdetect.FindingStateOpen {
		t.Fatalf("Release(box-a) = kept_open=%t, finding %+v; want kept open while box-b's throttle stands", kept, f)
	}
	if row, err := openAction(fx, "box-b"); err != nil || row.Action != pb.ProcessAction_PROCESS_ACTION_THROTTLE {
		t.Fatalf("box-b's action after box-a's release = %+v, %v; want THROTTLE still open", row, err)
	}
	if d := auditDetails(fx.audit, "release"); len(d) != 1 || !strings.Contains(d[0], "finding_kept_open=true") {
		t.Fatalf("release audit = %v, want finding_kept_open=true", d)
	}

	f, kept, err = fx.r.Release(ctx, "box-b")
	if err != nil {
		t.Fatal(err)
	}
	if kept || f == nil || f.State != threatdetect.FindingStateResolved {
		t.Fatalf("Release(box-b) = kept_open=%t, finding %+v; want resolved: it was the last box", kept, f)
	}
}

// TestQueuedResponse_ReleaseOfAnotherFindingDoesNotSkip (#2772 review 2):
// box-a is throttled for a mining-behavior finding; a signature finding with
// kill_process then fires on box-a and queues; the operator releases the
// mining-behavior throttle after that. The release belongs to the other
// finding, so the signature job must still run: the miner is killed and the
// signature finding gets its own action.
func TestQueuedResponse_ReleaseOfAnotherFindingDoesNotSkip(t *testing.T) {
	ctx := context.Background()
	fx, sink := queuedThrottle(t)
	first := fx.fire(t, "box-a", ruleMiningBehavior, pb.ThreatSeverity_THREAT_SEVERITY_HIGH) // sync: throttled now
	if row, err := openAction(fx, "box-a"); err != nil || row.FindingID != first.ID {
		t.Fatalf("open action after the mining-behavior finding = %+v, %v", row, err)
	}
	fx.setKill(t, "", true)
	killer := &fakeKiller{}
	fx.r.SetProcessKiller(killer)
	job := queueSignature(t, fx, sink, 4242)
	if job.f.ID == first.ID {
		t.Fatal("the signature finding must be a finding of its own")
	}
	fx.clock = fx.clock.Add(time.Minute)
	if _, _, err := fx.r.Release(ctx, "box-a"); err != nil { // releases the OTHER finding's throttle, after the job was queued
		t.Fatal(err)
	}

	fx.r.runJob(ctx, job)

	if len(killer.calls) != 1 || len(auditDetails(fx.audit, "kill")) != 1 {
		t.Fatalf("kills = %v, kill audits = %v; want one of each: the release was of another finding's action", killer.calls, auditDetails(fx.audit, "kill"))
	}
	if d := auditDetails(fx.audit, "response-skipped"); len(d) != 0 {
		t.Fatalf("response-skipped audit = %v, want none", d)
	}
	row, err := openAction(fx, "box-a")
	if err != nil || row.FindingID != job.f.ID || row.Action != pb.ProcessAction_PROCESS_ACTION_THROTTLE || row.KilledPID != 4242 {
		t.Fatalf("open action after the job = %+v, %v; want the signature finding's own THROTTLE with killed_pid 4242", row, err)
	}
	f, _ := fx.findings.Get(ctx, job.f.ID)
	if f.State != threatdetect.FindingStateOpen || f.Response == nil || f.Response.State != pb.ResponseState_RESPONSE_STATE_APPLIED || !f.Response.ProcessKilled {
		t.Fatalf("signature finding = state %s response %+v, want open, APPLIED, process_killed", f.State, f.Response)
	}
	if g, _ := fx.findings.Get(ctx, first.ID); g.State != threatdetect.FindingStateResolved {
		t.Fatalf("mining-behavior finding = %s, want resolved by its release", g.State)
	}
}

// TestQueuedResponse_AlertOnlyFindingStaysOpen preserves the single-box
// policy: without an operator decision, an alert-only finding stays open.
func TestQueuedResponse_AlertOnlyFindingStaysOpen(t *testing.T) {
	ctx := context.Background()
	fx, sink := queuedThrottle(t)
	if err := fx.policies.SetOverride(ctx, "box-a", &pb.ProcessPolicy{Action: pb.ProcessAction_PROCESS_ACTION_ALERT}); err != nil {
		t.Fatal(err)
	}
	job := queueFor(t, fx, sink, "box-a")
	fx.r.runJob(ctx, job)
	f, _ := fx.findings.Get(ctx, job.f.ID)
	if f.State != threatdetect.FindingStateOpen || f.Response == nil || f.Response.Action != pb.ProcessAction_PROCESS_ACTION_ALERT {
		t.Fatalf("finding = state %s response %+v; want open with an ALERT response", f.State, f.Response)
	}
	if d := auditDetails(fx.audit, "resolve"); len(d) != 0 {
		t.Fatalf("resolve audit = %v, want none: no operator released this finding", d)
	}
}

// flakyActions is an ActionStore whose ListByFinding (the worker's scoped
// release lookup for a queued job) fails the first `failures` times, as a
// Postgres hiccup would make it.
type flakyActions struct {
	threatdetect.ActionStore
	mu       sync.Mutex
	failures int
}

func (s *flakyActions) ListByFinding(ctx context.Context, id int64) ([]*threatdetect.SecurityAction, error) {
	s.mu.Lock()
	fail := s.failures > 0
	if fail {
		s.failures--
	}
	s.mu.Unlock()
	if fail {
		return nil, errors.New("pg: connection refused")
	}
	return s.ActionStore.ListByFinding(ctx, id)
}

// flakyFindings is a findingLifecycle whose Get fails the first `failures`
// times.
type flakyFindings struct {
	findingLifecycle
	mu       sync.Mutex
	failures int
}

func (s *flakyFindings) Get(ctx context.Context, id int64) (*threatdetect.Finding, error) {
	s.mu.Lock()
	fail := s.failures > 0
	if fail {
		s.failures--
	}
	s.mu.Unlock()
	if fail {
		return nil, errors.New("pg: connection refused")
	}
	return s.findingLifecycle.Get(ctx, id)
}

// TestQueuedResponse_StoreReadErrorDoesNotSkip (#2772 review 5): a store
// read that fails while the worker checks a queued job is neither a release
// nor a resolve. The job must go on: the locked read tries once more, and if
// the store is still down the job acts on its queued copy. A Postgres hiccup
// never costs the kill, which nothing would re-fire (the process does not
// exec again).
func TestQueuedResponse_StoreReadErrorDoesNotSkip(t *testing.T) {
	const forever = 1 << 20
	cases := []struct {
		name     string
		wire     func(fx *responderFixture, failures int)
		failures int
	}{
		{"action lookup fails once", wireFlakyActions, 1},
		{"action lookup fails every time", wireFlakyActions, forever},
		{"finding read fails once", wireFlakyFindings, 1},
		{"finding read fails every time", wireFlakyFindings, forever},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fx, sink := queuedThrottle(t)
			fx.setKill(t, "", true)
			killer := &fakeKiller{}
			fx.r.SetProcessKiller(killer)
			job := queueSignature(t, fx, sink, 4242)
			tc.wire(fx, tc.failures) // after the queueing write, which is not under test

			fx.r.runJob(ctx, job)

			if len(killer.calls) != 1 || len(auditDetails(fx.audit, "kill")) != 1 {
				t.Fatalf("kills = %v, kill audits = %v; want one of each: a read error is not a release", killer.calls, auditDetails(fx.audit, "kill"))
			}
			if d := auditDetails(fx.audit, "response-skipped"); len(d) != 0 {
				t.Fatalf("response-skipped audit = %v, want none", d)
			}
			row, err := openAction(fx, "box-a")
			if err != nil || row.Action != pb.ProcessAction_PROCESS_ACTION_THROTTLE || row.KilledPID != 4242 {
				t.Fatalf("open action = %+v, %v; want THROTTLE with killed_pid 4242", row, err)
			}
		})
	}
}

func wireFlakyActions(fx *responderFixture, failures int) {
	fx.r.actions = &flakyActions{ActionStore: fx.actions, failures: failures}
}

func wireFlakyFindings(fx *responderFixture, failures int) {
