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
