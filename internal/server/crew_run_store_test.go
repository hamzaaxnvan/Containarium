package server

import (
	"context"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// These run against MemCrewRunStore. The Postgres impl satisfies the same
// interface and is covered by the integration lane (a real DB), so the
// behavioral contract lives here once rather than being restated per backend.

func TestMemCrewRunStore_PutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewMemCrewRunStore()

	want := &pb.CrewRun{
		Id:        "crewrun-abc",
		CrewId:    "research",
		TraceId:   "abc",
		State:     pb.CrewRunState_CREW_RUN_STATE_RUNNING,
		InputJson: `{"q":"hello"}`,
	}
	if err := s.Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := s.Get(ctx, "crewrun-abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("run not found after Put")
	}
	if got.GetCrewId() != "research" || got.GetState() != pb.CrewRunState_CREW_RUN_STATE_RUNNING {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if got.GetInputJson() != `{"q":"hello"}` {
		t.Errorf("input_json = %q", got.GetInputJson())
	}
}

func TestMemCrewRunStore_GetMissing(t *testing.T) {
	got, ok, err := NewMemCrewRunStore().Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok || got != nil {
		t.Errorf("want (nil,false), got (%v,%v)", got, ok)
	}
}

// The store must clone on the way in. RunCrew records a run and then keeps
// mutating the same pointer as the crew progresses; if the store held that
// pointer, a reader would observe fields mid-write.
func TestMemCrewRunStore_ClonesOnPut(t *testing.T) {
	ctx := context.Background()
	s := NewMemCrewRunStore()

	run := &pb.CrewRun{Id: "r1", State: pb.CrewRunState_CREW_RUN_STATE_RUNNING}
	if err := s.Put(ctx, run); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Mutate the caller's copy exactly as RunCrew does after recording.
	run.State = pb.CrewRunState_CREW_RUN_STATE_FAILED
	run.Error = "boom"

	got, _, err := s.Get(ctx, "r1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetState() != pb.CrewRunState_CREW_RUN_STATE_RUNNING {
		t.Errorf("stored run tracked a post-Put mutation: state = %v, want RUNNING", got.GetState())
	}
