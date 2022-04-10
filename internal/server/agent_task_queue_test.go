package server

import (
	"sync"
	"testing"
	"time"
)

// newTestQueue returns a queue with a controllable clock starting at a fixed
// instant, plus a function to advance it.
func newTestQueue() (*agentTaskQueue, func(time.Duration)) {
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0).UTC()
	q := newAgentTaskQueue()
	q.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
	return q, advance
}

func TestQueue_EnqueueLeaseComplete(t *testing.T) {
	q, _ := newTestQueue()
	id := q.enqueue("web-researcher", `{"q":"hi"}`)
	if id == "" {
		t.Fatal("enqueue returned empty id")
	}
	if q.depth() != 1 {
		t.Fatalf("depth = %d, want 1", q.depth())
	}

	leased, ok := q.lease("", 0)
	if !ok {
		t.Fatal("expected a task to lease")
	}
	if leased.ID != id || leased.SkillID != "web-researcher" || leased.InputJSON != `{"q":"hi"}` {
		t.Fatalf("leased wrong task: %+v", leased)
	}
	if leased.LeaseToken == "" {
		t.Error("lease token should be non-empty")
	}

	if ok := q.complete(leased.ID, leased.LeaseToken, `{"answer":42}`, ""); !ok {
		t.Fatal("complete with valid token should succeed")
	}
	if q.depth() != 0 {
		t.Fatalf("depth after complete = %d, want 0", q.depth())
	}

	// The outcome is recorded and inspectable.
	r, ok := q.result(id)
	if !ok {
		t.Fatal("completed task should have a recorded result")
	}
	if r.artifactJSON != `{"answer":42}` || r.errMsg != "" || r.skillID != "web-researcher" {
		t.Errorf("recorded result wrong: %+v", r)
	}
}

func TestQueue_LeaseHidesFromOtherWorkers(t *testing.T) {
	q, _ := newTestQueue()
	q.enqueue("s", "a")

	if _, ok := q.lease("", time.Minute); !ok {
		t.Fatal("first lease should succeed")
	}
	// A second worker polling immediately sees nothing — the task is leased.
	if _, ok := q.lease("", time.Minute); ok {
		t.Fatal("second lease should find nothing (task is held)")
	}
}

func TestQueue_RedeliversAfterLeaseExpiry(t *testing.T) {
	q, advance := newTestQueue()
	q.enqueue("s", "a")

	first, ok := q.lease("", 30*time.Second)
	if !ok {
		t.Fatal("first lease should succeed")
	}
	// Still inside the lease window: invisible.
	advance(20 * time.Second)
	if _, ok := q.lease("", 30*time.Second); ok {
		t.Fatal("task should still be hidden inside the lease window")
	}
	// Past expiry: redelivered with a NEW token.
	advance(15 * time.Second)
	second, ok := q.lease("", 30*time.Second)
	if !ok {
		t.Fatal("task should be redelivered after lease expiry")
	}
	if second.LeaseToken == first.LeaseToken {
		t.Error("redelivery must mint a fresh lease token")
	}

	// The slow original worker now completes with its STALE token — rejected,
	// because the redelivered lease owns the task.
	if ok := q.complete(first.ID, first.LeaseToken, "late", ""); ok {
		t.Error("stale-token completion must be rejected")
	}
	// The current lease holder completes successfully.
	if ok := q.complete(second.ID, second.LeaseToken, "ok", ""); !ok {
		t.Error("current-token completion must succeed")
	}
}

func TestQueue_SkillFilter(t *testing.T) {
	q, _ := newTestQueue()
	q.enqueue("alpha", "1")
	q.enqueue("beta", "2")

	// A worker scoped to "beta" skips the alpha task at the head.
	leased, ok := q.lease("beta", time.Minute)
	if !ok || leased.SkillID != "beta" {
		t.Fatalf("filtered lease should return the beta task, got ok=%v %+v", ok, leased)
	}
