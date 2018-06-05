// Package retention stores the per-class retention policy and the per-org
// legal holds (#2576, slice A #2875; docs/security/DATA-FLOW-AND-RETENTION.md).
// The store itself deletes nothing; the sweeper in this package counts
// (#2876) and, once an admin switches the mode to ENFORCE, removes (#2877).
package retention

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
	// ErrInvalid wraps every rejected input (bad class, days < 1, empty org).
	ErrInvalid      = errors.New("invalid retention request")
	ErrHoldExists   = errors.New("org is already on legal hold")
	ErrHoldNotFound = errors.New("org has no legal hold")
)

// defaultDays are the windows of a class no admin has set (Q6 on #2727).
var defaultDays = map[pb.RetentionClass]int32{
	pb.RetentionClass_RETENTION_CLASS_PROMPTS:             30,
	pb.RetentionClass_RETENTION_CLASS_RESPONSES:           30,
	pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS:        90,
	pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS: 90,
	pb.RetentionClass_RETENTION_CLASS_EVIDENCE_BUNDLES:    365,
}

// Classes returns every class, in enum order.
func Classes() []pb.RetentionClass {
	out := make([]pb.RetentionClass, 0, len(defaultDays))
	for c := range defaultDays {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// PolicySetResult is what one SetPolicy replaced and stored.
type PolicySetResult struct {
	Previous, Current *pb.RetentionPolicy
}

// ModeSetResult is what one SetMode replaced and stored.
type ModeSetResult struct {
	Previous, Current *pb.RetentionModeState
}

// ValidateMode rejects UNSPECIFIED and unknown modes.
func ValidateMode(m pb.RetentionMode) error {
	switch m {
	case pb.RetentionMode_RETENTION_MODE_DRY_RUN, pb.RetentionMode_RETENTION_MODE_ENFORCE:
		return nil
	}
	return fmt.Errorf("%w: mode must be DRY_RUN or ENFORCE, got %v", ErrInvalid, m)
}

// BuildModeState renders a stored mode. A mode never set is the DRY_RUN
// default with no setter and no time. Exported for the Postgres store.
func BuildModeState(mode pb.RetentionMode, updatedAt *time.Time, updatedBy string) *pb.RetentionModeState {
	st := &pb.RetentionModeState{Mode: mode, UpdatedBy: updatedBy}
	if updatedAt != nil {
		st.UpdatedAt = timestamppb.New(*updatedAt)
	}
	return st
}

// Store keeps the policy, the mode and the holds. Each write takes a beforeCommit hook
// (nil = none) that runs with the pending change before it is committed; its
// error discards the change and is returned, so a caller can make a change
// conditional on its audit entry being written.
type Store interface {
	// GetPolicy returns a window for every class, set or default.
	GetPolicy(ctx context.Context) (*pb.RetentionPolicy, error)
	// SetPolicy changes the listed classes only; revision = previous + 1.
	SetPolicy(ctx context.Context, windows []*pb.RetentionWindow, updatedBy string, beforeCommit func(*PolicySetResult) error) (*PolicySetResult, error)
	// GetMode returns the retention mode: DRY_RUN until an admin sets it.
	GetMode(ctx context.Context) (*pb.RetentionModeState, error)
	// SetMode switches the mode; UNSPECIFIED or an unknown mode is ErrInvalid.
	SetMode(ctx context.Context, mode pb.RetentionMode, updatedBy string, beforeCommit func(*ModeSetResult) error) (*ModeSetResult, error)
	// ListHolds returns the active holds ordered by org id.
	ListHolds(ctx context.Context) ([]*pb.LegalHold, error)
	// SetHold places a hold; an org already on hold is ErrHoldExists.
	SetHold(ctx context.Context, orgID, reason, setBy string, beforeCommit func(*pb.LegalHold) error) (*pb.LegalHold, error)
	// ReleaseHold removes a hold and returns it; no hold is ErrHoldNotFound.
	ReleaseHold(ctx context.Context, orgID, releasedBy string, beforeCommit func(*pb.LegalHold) error) (*pb.LegalHold, error)
}

// ValidateWindows checks a SetPolicy request and returns it as class -> days.
