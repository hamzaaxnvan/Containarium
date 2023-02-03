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
	}
	if !found || res.artifactJSON != `{"ok":true}` {
		t.Errorf("result = %+v found=%v", res, found)
	}
}

// A leased task is hidden: a second worker polling immediately gets nothing,
// rather than the same task twice.
func TestMemAgentTaskQueue_LeasedTaskIsHidden(t *testing.T) {
	ctx := context.Background()
	q := NewMemAgentTaskQueue()
	if _, err := q.Enqueue(ctx, "s", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, _ := q.Lease(ctx, "", time.Minute); !ok {
		t.Fatal("first lease failed")
	}
	if _, ok, _ := q.Lease(ctx, "", time.Minute); ok {
		t.Error("a leased task was handed to a second worker")
	}
}

// The heart of the contract: a worker whose lease expired must not be able to
// complete a task that has since been redelivered to someone else.
func TestMemAgentTaskQueue_StaleLeaseTokenRejected(t *testing.T) {
	ctx := context.Background()
	q := NewMemAgentTaskQueue()
	id, err := q.Enqueue(ctx, "s", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	first, ok, _ := q.Lease(ctx, "", time.Nanosecond) // expires immediately
	if !ok {
		t.Fatal("first lease failed")
	}
	time.Sleep(2 * time.Millisecond)

	second, ok, _ := q.Lease(ctx, "", time.Minute) // redelivery
	if !ok {
		t.Fatal("expired lease was not redelivered")
	}
	if second.LeaseToken == first.LeaseToken {
		t.Fatal("redelivery reused the expired token; the check below would be vacuous")
	}

	accepted, err := q.Complete(ctx, id, first.LeaseToken, "late", "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if accepted {
		t.Error("a stale lease token completed a task that had been redelivered — " +
			"the slow worker clobbered the retry that overtook it")
	}

