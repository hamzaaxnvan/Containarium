package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Exercise the actual rule -> engine -> async sink -> responder path, rather
// than constructing subjects in a fake sink that could hide the dedupe bug.
func minerEngine(t *testing.T, fx *responderFixture, sink *ActingSink) *threatdetect.Engine {
	t.Helper()
	rule, err := threatdetect.NewMinerSignatureRule(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	engine := threatdetect.NewEngine(sink, "backend-test", false, nil, func() time.Time { return fx.clock })
	engine.Register(rule)
	return engine
}

func queueMiner(t *testing.T, fx *responderFixture, engine *threatdetect.Engine, box string) responseJob {
	t.Helper()
	engine.OnExec(threatdetect.ExecEvent{Container: box, TenantID: "tenant-a", PID: 4242,
		Filename: "/tmp/xmrig", Comm: "xmrig", Argv: []string{"xmrig"}})
	select {
	case job := <-fx.r.queue:
		return job
	default:
		t.Fatal("miner exec did not queue a response")
		return responseJob{}
	}
}

func TestMinerSignature_PerContainerRelease(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "both applied"
		if queued {
			name = "B queued when A released"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fx, sink := queuedThrottle(t)
			rx := withWebhook(t, fx)
			engine := minerEngine(t, fx, sink)
			a := queueMiner(t, fx, engine, "box-a")
			fx.r.runJob(ctx, a)
			b := queueMiner(t, fx, engine, "box-b")
			if a.f.ID == b.f.ID {
				t.Fatalf("two containers share finding %d", a.f.ID)
			}
			if !queued {
				fx.r.runJob(ctx, b)
			}
			released, kept, err := fx.r.Release(ctx, "box-a")
			if err != nil {
				t.Fatal(err)
			}
			if kept || released.ID != a.f.ID || released.State != threatdetect.FindingStateResolved {
				t.Fatalf("A release = %+v, kept=%t", released, kept)
			}
			if queued {
				fx.r.runJob(ctx, b)
			}
			storedB := mustGet(t, fx, b.f.ID)
			if storedB.State != threatdetect.FindingStateOpen || storedB.Response.State != pb.ResponseState_RESPONSE_STATE_APPLIED || storedB.Response.Container != "box-b" {
				t.Fatalf("B was changed by A's release: %+v", storedB)
			}
			row, err := openAction(fx, "box-b")
			if err != nil || row.FindingID != b.f.ID {
				t.Fatalf("B action = %+v, %v", row, err)
			}
			released, kept, err = fx.r.Release(ctx, "box-b")
			if err != nil || kept || released == nil || released.State != threatdetect.FindingStateResolved || released.ID != b.f.ID {
				t.Fatalf("last release = %+v, kept=%t, %v", released, kept, err)
			}
			if got := mustGet(t, fx, a.f.ID); got.Response.Container != "box-a" || got.Response.State != pb.ResponseState_RESPONSE_STATE_RELEASED {
				t.Fatalf("A overwritten by B: %+v", got.Response)
			}
			open, resolved := pb.FindingState_FINDING_STATE_OPEN, pb.FindingState_FINDING_STATE_RESOLVED
			want := []wantPost{
				{"A finding", open, "box-a", pb.ResponseState_RESPONSE_STATE_UNSPECIFIED},
				{"A applied", open, "box-a", pb.ResponseState_RESPONSE_STATE_APPLIED},
				{"B finding", open, "box-b", pb.ResponseState_RESPONSE_STATE_UNSPECIFIED},
