package tracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Dispatch store (#2022): one durable row per dispatch of a routed
// `scope:<role>` issue. Exactly-once is NOT application logic — it is the
// partial unique index tracker_dispatches_active: at most one queued or
// running row per (username, connection, issue_number), so two
// dispatchers (or one restarted mid-tick) racing on the same issue get
// one insert and one unique violation. See
// docs/architecture/issue-triggered-agents.md ("Exactly-once").

// Dispatch is the storage-layer view of a TrackerDispatch.
type Dispatch struct {
	ID          string
	Username    string
	Connection  string
	IssueNumber int64
	Scope       string
	SkillID     string
	// RunID is chosen by the dispatcher before the run starts, so the
	// run JWT's run_id claim and this row always agree.
	RunID string
	// Depth is the issue's lineage depth when it was dispatched, read
	// from tracker_issue_lineage (0 for a human-created issue).
	Depth         int32
	State         pb.TrackerDispatchState
	FailureReason string
	// Failure is the typed cause of a FAILED row (#2026); UNSPECIFIED
	// otherwise.
	Failure pb.TrackerDispatchFailure
	// LabelsPending is set when the forge may not show State yet: a
	// projection failed, or the row is terminal and its projection has
	// not landed (set with the terminal transition, cleared once it
	// lands). An issue whose latest row is terminal and pending is never
	// dispatched again, and every tick retries the projection (#2047,
	// #2052).
	LabelsPending bool
	// CreatedAt is when the row was inserted — immediately before the
	// issue is labelled agent:queued, so it is the "label applied" end of
	// the latency metric. InsertDispatch takes it from the caller's clock
	// when set, so every timestamp on a row comes from one clock.
	CreatedAt time.Time
	StartedAt time.Time
	EndedAt   time.Time
	// ChangeURL / ChangeNumber are the change request the run submitted
	// through SubmitTrackerChange (#3001), recorded by RecordDispatchChange
	// against the row's run id; empty / zero until then.
	ChangeURL    string
	ChangeNumber int64
}

// ErrDispatchActive is returned by InsertDispatch when the issue already
// has a queued or running dispatch — the exactly-once index said no. The
// caller skips the issue; it is not a failure.
var ErrDispatchActive = errors.New("tracker: issue already has an active dispatch")

// ErrDispatchNotFound is returned by GetDispatch for an unknown id.
var ErrDispatchNotFound = errors.New("tracker: dispatch not found")

// ErrInvalidDispatchTransition is returned by TransitionDispatch for a
// (from, to) pair the state machine never allows (anything out of a
// terminal state, or backwards).
var ErrInvalidDispatchTransition = errors.New("tracker: invalid dispatch state transition")

// pgUniqueViolation is SQLSTATE 23505.
const pgUniqueViolation = "23505"

// dispatchSchema is applied from Store.initSchema after
// tracker_connections. Deleting a connection drops its dispatch history
// and warnings, same as its routes.
const dispatchSchema = `
	CREATE TABLE IF NOT EXISTS tracker_dispatches (
		id             UUID PRIMARY KEY,
		seq            BIGSERIAL NOT NULL,
		username       TEXT NOT NULL,
		connection     TEXT NOT NULL,
		issue_number   BIGINT NOT NULL,
		scope          TEXT NOT NULL,
		skill_id       TEXT NOT NULL,
		run_id         TEXT NOT NULL DEFAULT '',
		depth          INT NOT NULL DEFAULT 0,
		state          TEXT NOT NULL CHECK (state IN ('queued', 'running', 'done', 'failed')),
		failure_reason TEXT NOT NULL DEFAULT '',
		labels_pending BOOLEAN NOT NULL DEFAULT false,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		started_at     TIMESTAMPTZ,
		ended_at       TIMESTAMPTZ,
		FOREIGN KEY (username, connection) REFERENCES tracker_connections (username, name) ON DELETE CASCADE
	);

	-- The typed cause of a failed row (#2026); '' when not failed.
	ALTER TABLE tracker_dispatches ADD COLUMN IF NOT EXISTS failure TEXT NOT NULL DEFAULT '';

	-- The change request the run submitted (#3001); '' / 0 until it does.
	ALTER TABLE tracker_dispatches ADD COLUMN IF NOT EXISTS change_url TEXT NOT NULL DEFAULT '';
	ALTER TABLE tracker_dispatches ADD COLUMN IF NOT EXISTS change_number BIGINT NOT NULL DEFAULT 0;

	-- The exactly-once guarantee (#2022).
	CREATE UNIQUE INDEX IF NOT EXISTS tracker_dispatches_active
		ON tracker_dispatches (username, connection, issue_number)
		WHERE state IN ('queued', 'running');

	CREATE INDEX IF NOT EXISTS tracker_dispatches_by_connection
		ON tracker_dispatches (username, connection, seq DESC);

	-- One stamped warning per (issue, unrouted scope), ever — not one
	-- per tick.
	CREATE TABLE IF NOT EXISTS tracker_dispatch_warnings (
		username     TEXT NOT NULL,
		connection   TEXT NOT NULL,
		issue_number BIGINT NOT NULL,
		scope        TEXT NOT NULL,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (username, connection, issue_number, scope),
		FOREIGN KEY (username, connection) REFERENCES tracker_connections (username, name) ON DELETE CASCADE
	);
`

// dispatchStateToString / dispatchStateFromString convert at the store
// boundary; every caller above sees the typed enum.
func dispatchStateToString(s pb.TrackerDispatchState) (string, error) {
	switch s {
	case pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_QUEUED:
		return "queued", nil
	case pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING:
		return "running", nil
	case pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_DONE:
		return "done", nil
	case pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED:
		return "failed", nil
	default:
		return "", fmt.Errorf("tracker: unknown dispatch state %v", s)
	}
}

func dispatchStateFromString(s string) pb.TrackerDispatchState {
	switch s {
	case "queued":
		return pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_QUEUED
	case "running":
		return pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING
	case "done":
		return pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_DONE
	case "failed":
		return pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED
	default:
		return pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_UNSPECIFIED
	}
}

// dispatchFailureToString / dispatchFailureFromString convert the typed
// failure cause at the store boundary.
func dispatchFailureToString(f pb.TrackerDispatchFailure) (string, error) {
	switch f {
	case pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_START_ERROR:
		return "start_error", nil
	case pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_RUN_ERROR:
		return "run_error", nil
	case pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_TIMEOUT:
		return "timeout", nil
	case pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_LEASE_LOST:
		return "lease_lost", nil
	default:
		return "", fmt.Errorf("tracker: unknown dispatch failure %v", f)
	}
}

func dispatchFailureFromString(s string) pb.TrackerDispatchFailure {
	switch s {
	case "start_error":
		return pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_START_ERROR
	case "run_error":
		return pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_RUN_ERROR
	case "timeout":
		return pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_TIMEOUT
	case "lease_lost":
		return pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_LEASE_LOST
	default:
		return pb.TrackerDispatchFailure_TRACKER_DISPATCH_FAILURE_UNSPECIFIED
	}
}

// validDispatchTransition is the state machine: out of an active state
// only, forward only. queued->done is allowed (a run can end before
// anything marked it running).
func validDispatchTransition(from, to pb.TrackerDispatchState) bool {
	const (
		queued  = pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_QUEUED
		running = pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING
		done    = pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_DONE
		failed  = pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_FAILED
	)
	switch from {
	case queued:
		return to == running || to == done || to == failed
	case running:
		return to == done || to == failed
	default:
		return false
	}
}
