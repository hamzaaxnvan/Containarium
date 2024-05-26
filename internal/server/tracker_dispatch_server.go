package server

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Tracker dispatch (#2022): one tick of the dispatcher per
// DispatchTrackerIssues call, plus the dispatch-row listing. Both are
// tracker:admin; dispatch additionally needs agents:run because it
// starts runs on the caller's behalf. See
// docs/architecture/issue-triggered-agents.md.

// SetTrackerRunStarter wires the launcher the dispatcher starts runs
// through — in production, trackerRunStarter over the same
// AgentSkillServer that serves RunAgentSkill. Nil keeps
// DispatchTrackerIssues returning Unavailable.
func (s *ContainerServer) SetTrackerRunStarter(r tracker.RunStarter) {
	s.trackerRunStarter = r
}

// DispatchTrackerIssues runs one dispatcher tick for a connection.
func (s *ContainerServer) DispatchTrackerIssues(ctx context.Context, req *pb.DispatchTrackerIssuesRequest) (*pb.DispatchTrackerIssuesResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTrackerAdmin); err != nil {
		return nil, err
	}
	// Checked up front: without it every routed issue would become a
	// failed dispatch instead of one clear error.
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRun); err != nil {
		return nil, err
	}
	if err := s.requireTrackerRouteAccess(ctx, req.Username, req.Connection); err != nil {
		return nil, err
	}
	// Rejected here, before any store or forge write: a platform admin
	// passes the tenant check, but the run starter mints for the CALLER's
	// tenant and would refuse every issue — turning a wrong username
	// into N failed rows, N agent:failed labels and N public comments on
	// the other tenant's forge.
	if err := requireDispatchCaller(ctx, req.Username); err != nil {
		return nil, err
	}
	if req.MaxStarts < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "max_starts must be 0 (unlimited) or positive, got %d", req.MaxStarts)
	}
	if s.trackerRunStarter == nil {
		return nil, status.Error(codes.Unavailable, "tracker dispatch is not configured on this daemon (no run starter)")
	}
	// resolveWriterConn's two halves, split so the connection record
	// also yields the run's repository URL.
	if err := enforceConnectionBinding(ctx, req.Connection); err != nil {
		return nil, err
	}
	record, err := s.trackerStore.Get(ctx, req.Username, req.Connection)
	if err != nil {
		return nil, mapTrackerError(err)
	}
	provider, conn, err := s.writerConnFor(ctx, record)
	if err != nil {
		return nil, err
	}

	// The connection's policy, re-read every tick: a max_depth lowered
	// after a chain was filed still stops the deeper issues (#2025). The
	// same value's RunTimeout also bounds the sweep below (#2026).
	policy := tracker.PolicyFromProto(record.Policy)

	// The credential resolved above only proves it resolves before any
	// write; every forge call — including RunEnded and the sweep, up to
	// the run timeout after this tick — resolves it again through the
	// secrets store, so a rotated or short-lived credential is never
	// reused stale (#2269).
