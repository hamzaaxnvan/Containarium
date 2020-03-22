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
