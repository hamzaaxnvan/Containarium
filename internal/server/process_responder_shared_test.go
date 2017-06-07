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

