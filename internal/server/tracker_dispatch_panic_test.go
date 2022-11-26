package server

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// A panic in a dispatched run's background half (#2050) must not skip
// the lease end, the JWT revocation or the completion report, and must
// not take the daemon down with it.

// panicPayload stands in for anything a panicking agent seam could carry
// (a token, a prompt). It must never reach the outcome or the row.
const panicPayload = "secret-in-panic-payload"

func panickingAgent(string, string) (string, error) { panic(panicPayload) }

// TestFinishDispatchedRun_AgentPanicEndsLeaseAndFailsRun: the panic is
// recovered inside finishDispatchedRun. The lease still ends first (both
// JTIs revoked, run unregistered), then the run is reported failed — the
// same order as the normal path — and the payload is not in the outcome.
func TestFinishDispatchedRun_AgentPanicEndsLeaseAndFailsRun(t *testing.T) {
	const runID = "run-hook-panic"
	s, registry, store := newFinishHarness(t, runID)
	lc := newRecordingLifecycle()
	lc.registry, lc.runID = registry, runID

	var escaped any
	func() {
		defer func() { escaped = recover() }()
		s.finishDispatchedRun(ctxAs("alice", true), &startedSkillRun{runID: runID, lease: testLease(runID)}, panickingAgent, lc)
	}()
	if escaped != nil {
		t.Fatalf("the agent's panic escaped finishDispatchedRun (%v): in its goroutine that kills the daemon", escaped)
	}

	events, outcome, _ := lc.snapshot()
	if strings.Join(events, ",") != "ended" {
		t.Fatalf("events = %v, want exactly one end report", events)
	}
	if outcome.Err == nil {
		t.Fatal("outcome = success, want a failed run")
	}
	if strings.Contains(outcome.Err.Error(), panicPayload) {
		t.Errorf("outcome %q carries the panic payload", outcome.Err)
	}
	if lc.liveAtEnd {
		t.Error("run still live when its end was reported; the lease must end first")
	}
	if registry.Live(runID) {
		t.Error("run still registered after the panic")
	}
	for _, jti := range []string{"jti-platform", "jti-gateway"} {
		if revoked, err := store.IsRevoked(context.Background(), jti); err != nil || !revoked {
			t.Errorf("%s revoked = %v (err %v), want true after the panicking run", jti, revoked, err)
		}
	}
}

// TestLaunchDispatchedRun_AgentPanicDoesNotKillDaemon goes through the
// real background goroutine. Without a recover there this test binary
// dies, which is what the daemon would do.
func TestLaunchDispatchedRun_AgentPanicDoesNotKillDaemon(t *testing.T) {
	const runID = "run-hook-panic-goroutine"
	s, registry, _ := newFinishHarness(t, runID)
	lc := newRecordingLifecycle()
	s.launchDispatchedRun(ctxAs("alice", true), &startedSkillRun{runID: runID, lease: testLease(runID)}, lc, panickingAgent)
	select {
	case <-lc.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the run's end was never reported after the agent panicked")
	}
	events, outcome, _ := lc.snapshot()
	if strings.Join(events, ",") != "started,ended" || outcome.Err == nil {
		t.Fatalf("events = %v, outcome = %v; want started then a failed end", events, outcome.Err)
	}
