package server

import (
	"fmt"
	"sync"
	"time"
)

// agent_task_queue.go — in-memory lease queue backing the pull-based agent run
// model (prototype). It is deliberately a small, self-contained, lock-guarded
// type so its semantics can be unit-tested without a daemon, a box, or a DB:
//
//   - enqueue appends a task (FIFO).
//   - lease hands out the oldest *visible* task and hides it for a lease
//     window (SQS-style visibility timeout), minting a fresh lease token.
//   - a task whose lease window elapsed becomes visible again on the next
//     lease scan (redelivery — crash recovery for a worker that died mid-run).
//   - complete removes a task iff the caller presents the *current* lease
//     token; a token from an expired-then-redelivered lease is rejected, so a
//     slow worker can't clobber the retry that overtook it.
//
// Durability (survive daemon restart), per-tenant fairness, and dead-letter
// handling are follow-ups; Phase 0 is memory-only. See the pull-queue section
// of docs/AGENT-MODEL-GATEWAY-DESIGN.md.

// defaultLeaseDuration is used when a lease request passes 0. Chosen longer
// than a typical agent run so a healthy worker finishes well inside its lease.
const defaultLeaseDuration = 5 * time.Minute

// queuedTask is one task and its lease state. Visible (leasable) means
// leaseToken == "" (never leased) or now >= leaseDeadline (lease expired).
type queuedTask struct {
	id        string
	skillID   string
	inputJSON string

	leaseToken    string    // current lease holder's token; "" when never leased
	leaseDeadline time.Time // when the current lease expires
}

// taskResult is a completed task's recorded outcome (kept for inspection/audit
// in the prototype; a real impl would emit an event / write a rollup).
type taskResult struct {
	skillID      string
	artifactJSON string
	errMsg       string
	completedAt  time.Time
}

// agentTaskQueue is a FIFO lease queue. All operations are safe for concurrent
// callers (many worker boxes polling at once).
type agentTaskQueue struct {
	mu      sync.Mutex
	tasks   []*queuedTask         // pending + leased, in enqueue order
	results map[string]taskResult // completed, by task id
	seq     uint64                // monotonic; backs task ids and lease tokens
	now     func() time.Time      // injectable clock (tests override)
}

func newAgentTaskQueue() *agentTaskQueue {
	return &agentTaskQueue{
		results: make(map[string]taskResult),
		now:     time.Now,
	}
}

// enqueue adds a task and returns its server-assigned id.
func (q *agentTaskQueue) enqueue(skillID, inputJSON string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	id := fmt.Sprintf("task-%d", q.seq)
	q.tasks = append(q.tasks, &queuedTask{id: id, skillID: skillID, inputJSON: inputJSON})
	return id
}
