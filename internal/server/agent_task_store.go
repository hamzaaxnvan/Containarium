package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentTaskQueue is the pull-based run queue's storage contract (#1182).
// Two impls, selected as NetworkPolicyStore and CrewRunStore are:
// PostgresAgentTaskQueue when the daemon has a pool, MemAgentTaskQueue for
// --standalone daemons and tests.
//
// The lease semantics are the whole contract and both impls must match:
//
//   - Enqueue appends (FIFO).
//   - Lease hands out the oldest *visible* task and hides it for a window,
//     minting a fresh token. Visible means never leased, or the lease
//     deadline has passed.
//   - Complete removes a task only if the caller presents the *current*
//     token, so a slow worker cannot clobber the retry that overtook it.
//
// One thing the durable impl must get right that the in-memory one never
// had to: **lease tokens have to stay unique across a daemon restart.** The
// in-memory queue mints them from a counter that resets to zero on start, so
// after a restart it would happily re-mint "lease-1" — and a worker holding
// the pre-restart "lease-1" would then be accepted as the current owner,
// completing a task that had already been redelivered to someone else. The
// Postgres impl draws tokens from a sequence, which does not reset. That is
// what makes "a pre-restart lease token is rejected" hold rather than being
// an accident of timing.
type AgentTaskQueue interface {
	Enqueue(ctx context.Context, skillID, inputJSON string) (string, error)
	Lease(ctx context.Context, skillFilter string, d time.Duration) (leasedTask, bool, error)
	Complete(ctx context.Context, taskID, leaseToken, artifactJSON, errMsg string) (bool, error)
	Depth(ctx context.Context) (int, error)
	Result(ctx context.Context, taskID string) (taskResult, bool, error)
}

// --- in-memory ------------------------------------------------------

// MemAgentTaskQueue adapts the existing lock-guarded queue to the interface.
// Tasks do not survive a daemon restart.
type MemAgentTaskQueue struct{ q *agentTaskQueue }

func NewMemAgentTaskQueue() *MemAgentTaskQueue {
	return &MemAgentTaskQueue{q: newAgentTaskQueue()}
}

func (m *MemAgentTaskQueue) Enqueue(_ context.Context, skillID, inputJSON string) (string, error) {
	return m.q.enqueue(skillID, inputJSON), nil
}

func (m *MemAgentTaskQueue) Lease(_ context.Context, skillFilter string, d time.Duration) (leasedTask, bool, error) {
	t, ok := m.q.lease(skillFilter, d)
	return t, ok, nil
}

func (m *MemAgentTaskQueue) Complete(_ context.Context, taskID, leaseToken, artifactJSON, errMsg string) (bool, error) {
	return m.q.complete(taskID, leaseToken, artifactJSON, errMsg), nil
}

func (m *MemAgentTaskQueue) Depth(_ context.Context) (int, error) { return m.q.depth(), nil }

func (m *MemAgentTaskQueue) Result(_ context.Context, taskID string) (taskResult, bool, error) {
	r, ok := m.q.result(taskID)
	return r, ok, nil
}

// --- postgres -------------------------------------------------------

// PostgresAgentTaskQueue persists tasks so a daemon restart does not lose
// them.
type PostgresAgentTaskQueue struct{ pool *pgxpool.Pool }

// NewPostgresAgentTaskQueue creates the tables and the token sequence.
func NewPostgresAgentTaskQueue(ctx context.Context, pool *pgxpool.Pool) (*PostgresAgentTaskQueue, error) {
	const schema = `
		CREATE TABLE IF NOT EXISTS agent_tasks (
			id TEXT PRIMARY KEY,
			skill_id TEXT NOT NULL,
			input_json TEXT NOT NULL DEFAULT '',
			lease_token TEXT,
			lease_deadline TIMESTAMPTZ,
			enqueued_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		-- Lease scans read (visible, oldest-first) filtered by skill.
		CREATE INDEX IF NOT EXISTS agent_tasks_lease_idx
			ON agent_tasks (skill_id, enqueued_at);

		CREATE TABLE IF NOT EXISTS agent_task_results (
			id TEXT PRIMARY KEY,
			skill_id TEXT NOT NULL,
			artifact_json TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		-- Ids and lease tokens come from sequences rather than a counter in
		-- process memory, so neither restarts at zero. A reused lease token
		-- would let a worker holding a pre-restart lease complete a task that
		-- had since been redelivered.
		CREATE SEQUENCE IF NOT EXISTS agent_task_id_seq;
		CREATE SEQUENCE IF NOT EXISTS agent_lease_token_seq;
	`
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("init agent_tasks schema: %w", err)
	}
	return &PostgresAgentTaskQueue{pool: pool}, nil
}

func (p *PostgresAgentTaskQueue) Enqueue(ctx context.Context, skillID, inputJSON string) (string, error) {
	const q = `
		INSERT INTO agent_tasks (id, skill_id, input_json)
		VALUES ('task-' || nextval('agent_task_id_seq'), $1, $2)
		RETURNING id
