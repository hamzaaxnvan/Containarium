package server

import (
	"context"
	"testing"
	"time"
)

// The lease contract is what both impls owe. These run against
// MemAgentTaskQueue; PostgresAgentTaskQueue satisfies the same interface and
// is covered by the integration lane, where a real DB can exercise
// FOR UPDATE SKIP LOCKED and survive a restart.

func TestMemAgentTaskQueue_EnqueueLeaseComplete(t *testing.T) {
	ctx := context.Background()
	q := NewMemAgentTaskQueue()

	id, err := q.Enqueue(ctx, "summarize", `{"doc":"a"}`)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	leased, ok, err := q.Lease(ctx, "", 0)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if !ok {
		t.Fatal("nothing leasable after Enqueue")
	}
	if leased.ID != id || leased.SkillID != "summarize" || leased.InputJSON != `{"doc":"a"}` {
		t.Errorf("lease returned %+v, want the enqueued task", leased)
	}
	if leased.LeaseToken == "" {
		t.Error("lease minted no token")
	}

	accepted, err := q.Complete(ctx, id, leased.LeaseToken, `{"ok":true}`, "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !accepted {
		t.Fatal("Complete rejected the current lease token")
	}

	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("Depth: %v", err)
	}
	if depth != 0 {
		t.Errorf("depth = %d after Complete, want 0", depth)
	}

	res, found, err := q.Result(ctx, id)
	if err != nil {
		t.Fatalf("Result: %v", err)
