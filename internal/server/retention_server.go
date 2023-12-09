package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/retention"
	retentionpg "github.com/footprintai/containarium/internal/retention/pgstore"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Audit actions written by RetentionService changes (#2875).
const (
	retentionPolicySetAction   = "retention.policy.set"
	retentionHoldSetAction     = "retention.hold.set"
	retentionHoldReleaseAction = "retention.hold.release"
	// retentionModeSetAction and retentionSweepAction are the audit actions of
	// the mode switch and of an enforced sweep (#2877).
	retentionModeSetAction = "retention.mode.set"
	retentionSweepAction   = "retention.sweep"
	// retentionSweepResultAction is the second entry of a purge of the
	// hash-chained audit_logs (#2979): the retention.sweep entry is written
	// before the purge and can name only the chain base it started from, so
	// this one, written after, names the base it ended on.
	retentionSweepResultAction = "retention.sweep.result"
)

// retentionSweeperActor is the username on the audit entry of an enforced
// sweep, which no admin runs.
const retentionSweeperActor = "retention-sweeper"

// retentionModeAuditDetail is the Detail of a retention.mode.set entry.
type retentionModeAuditDetail struct {
	PreviousMode string `json:"previous_mode"`
	Mode         string `json:"mode"`
}

// retentionSweepAuditStore is one store in a retention.sweep entry: counts
// only, never the content of a record.
type retentionSweepAuditStore struct {
	Class      string `json:"class"`
	Store      string `json:"store"`
	WindowDays int32  `json:"window_days"`
	Eligible   int64  `json:"eligible"`
	Held       int64  `json:"held"`
	// PreviousBaseID is the chain base the sweep starts from, for the
	// audit_logs store; absent when the store has no chain base (#2979).
	PreviousBaseID int64 `json:"previous_base_id,omitempty"`
}

// retentionSweepAuditDetail is the Detail of a retention.sweep entry, written
// before an enforced sweep removes anything. Eligible is what the sweep will
// try to remove; a store with a backlog above retention.MaxRemovePerStore is
// worked off over the following sweeps.
type retentionSweepAuditDetail struct {
	PolicyRevision int64                      `json:"policy_revision"`
	ActiveHolds    int64                      `json:"active_holds"`
	Stores         []retentionSweepAuditStore `json:"stores"`
}

// retentionSweepResultClass is the rows one class lost in a purge.
type retentionSweepResultClass struct {
	Class   string `json:"class"`
	Removed int64  `json:"removed"`
}

// retentionSweepResultAuditDetail is the Detail of a retention.sweep.result
// entry: ids and counts only, never the content of a row (#2979).
type retentionSweepResultAuditDetail struct {
	Store          string                      `json:"store"`
	PreviousBaseID int64                       `json:"previous_base_id,omitempty"`
	BaseID         int64                       `json:"base_id"`
	RowsRemoved    int64                       `json:"rows_removed"`
	Classes        []retentionSweepResultClass `json:"classes"`
}

// retentionAuditWindow is one changed class in a retention.policy.set entry.
type retentionAuditWindow struct {
	Class string `json:"class"`
	Days  int32  `json:"days"`
