package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// trackerWriterFor resolves provider to its WriterProvider — the same
// resolution and test-override mechanism trackerProviderFor uses (both
// real adapters, and any fake a test registers via SetTrackerDescribers,
// satisfy WriterProvider; the map is declared as ReaderProvider only
// because #1921's read-only callers never needed more).
func (s *ContainerServer) trackerWriterFor(provider pb.TrackerProvider) (tracker.WriterProvider, error) {
	rp, err := s.trackerProviderFor(provider)
	if err != nil {
		return nil, err
	}
	wp, ok := rp.(tracker.WriterProvider)
	if !ok {
		return nil, fmt.Errorf("provider %v does not support write verbs", provider)
	}
	return wp, nil
}

// resolveWriterConn is resolveReaderConn's write-verb counterpart:
// resolves a named connection to its WriterProvider adapter and a
// ready-to-use tracker.Conn. Tenant scoping and the tracker:write scope
// are the caller's responsibility. Also enforces the run <-> connection
// JWT-claim binding (#1922 step 6) — see enforceConnectionBinding's own
// doc comment — same as the read verbs.
func (s *ContainerServer) resolveWriterConn(ctx context.Context, username, connectionName string) (tracker.WriterProvider, tracker.Conn, error) {
	if err := enforceConnectionBinding(ctx, connectionName); err != nil {
		return nil, tracker.Conn{}, err
	}
	trackerConn, err := s.trackerStore.Get(ctx, username, connectionName)
	if err != nil {
		return nil, tracker.Conn{}, mapTrackerError(err)
	}
	return s.writerConnFor(ctx, trackerConn)
}

// writerConnFor is resolveWriterConn's second half: adapter + credential
// for an already-loaded connection record. Split out (#2024) so verbs
// that need the record itself first — CreateTrackerIssue and
// SetTrackerIssueLabels read its policy to allow-list labels BEFORE
// resolving a credential or touching the tracker — share the same
// resolution as the rest.
func (s *ContainerServer) writerConnFor(ctx context.Context, trackerConn *tracker.Connection) (tracker.WriterProvider, tracker.Conn, error) {
	provider, err := s.trackerWriterFor(trackerConn.Provider)
	if err != nil {
		return nil, tracker.Conn{}, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	if s.secretsStore == nil {
		return nil, tracker.Conn{}, status.Error(codes.Unavailable, "secrets store not configured on this daemon")
	}
	cred, err := s.secretsStore.BrokerCredential(ctx, trackerConn.Username, trackerConn.CredentialSecret)
	if err != nil {
		return nil, tracker.Conn{}, status.Errorf(codes.FailedPrecondition, "resolve broker credential: %v", err)
	}
	return provider, tracker.Conn{
		BaseURL:    trackerConn.BaseURL,
		Project:    trackerConn.Project,
		Credential: cred,
	}, nil
}

// identityFromContext resolves the platform identity to stamp on a
// brokered write, built only from the verified token's claims and (for
// a run-scoped token) the run registry — never from a request field,
// per the design note's anti-forgery invariant for the identity stamp.
//
// A run-scoped token names its run via the run_id claim (#1922); its
// skill and model are resolved from the shared run registry, which
// AgentSkillServer populates at RunAgentSkill and clears at lease end.
// An unknown run id (e.g. the registry was restarted since the run
// began) still stamps the run id itself, just with an empty skill/model
// — best-effort, never a reason to fail the write.
//
// An operator/human token carries no run_id claim at all. The design
// note frames every brokered write as something an agent run does, but
// the CLI's own tracker issue comment/claim/label commands are meant to
// work from a human's own token too. Stamped as "operator/<username>"
// instead: still exactly what the caller authenticated as.
func (s *ContainerServer) identityFromContext(ctx context.Context) tracker.Identity {
	runID, hasRun := auth.RunIDFromGRPCContext(ctx)
	if !hasRun || runID == "" {
		username, _, _ := auth.SubjectFromGRPCContext(ctx)
		return tracker.Identity{RunID: username, SkillID: "operator"}
	}
	var info runlease.Info
	if s.runRegistry != nil {
		info, _ = s.runRegistry.Get(runID)
	}
	return tracker.Identity{RunID: runID, SkillID: info.SkillID, Model: info.Model}
}

// CommentOnTrackerIssue posts a stamped, sanitized comment on an issue
// or change request.
func (s *ContainerServer) CommentOnTrackerIssue(ctx context.Context, req *pb.CommentOnTrackerIssueRequest) (*pb.CommentOnTrackerIssueResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTrackerWrite); err != nil {
		return nil, err
	}
	if s.trackerStore == nil {
		return nil, status.Error(codes.Unavailable, "tracker store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeTrackerTenant(ctx, req.Username); err != nil {
		return nil, err
	}
	if req.Body == "" {
		return nil, status.Error(codes.InvalidArgument, "body is required")
	}

	provider, conn, err := s.resolveWriterConn(ctx, req.Username, req.Connection)
	if err != nil {
		return nil, err
	}

	id := s.identityFromContext(ctx)
	body := tracker.Sanitize(req.Body) + "\n\n" + tracker.Stamp(id, tracker.KindComment)
	comment, err := provider.Comment(ctx, conn, req.Number, body)
	if err != nil {
		return nil, mapProviderError(err)
	}

	s.auditTrackerWrite(ctx, "tracker.comment", req.Username, req.Connection, req.Number, trackerCommentAuditDetail{
		Connection: req.Connection,
		Number:     req.Number,
		RunID:      id.RunID,
		SkillID:    id.SkillID,
	})
	return &pb.CommentOnTrackerIssueResponse{Comment: toProtoComment(comment)}, nil
}

// runResolver returns the tracker.RunResolver ClaimTrackerIssue uses to
// decide whether a foreign claim's run is still going. A daemon with no
// run registry wired (tests constructing ContainerServer directly)
// falls back to a resolver that reports every run as not-live, which
// degrades ClaimTrackerIssue to freshness-only — safe, just less
// precise than with the registry.
func (s *ContainerServer) runResolver() tracker.RunResolver {
	if s.runRegistry != nil {
		return s.runRegistry
	}
	return noRunsLiveResolver{}
}

type noRunsLiveResolver struct{}

func (noRunsLiveResolver) Live(string) bool { return false }

// ClaimTrackerIssue attempts to claim an issue for the calling run.
func (s *ContainerServer) ClaimTrackerIssue(ctx context.Context, req *pb.ClaimTrackerIssueRequest) (*pb.ClaimTrackerIssueResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTrackerWrite); err != nil {
		return nil, err
	}
	if s.trackerStore == nil {
		return nil, status.Error(codes.Unavailable, "tracker store not configured on this daemon")
	}
	if s.claimLocks == nil {
		return nil, status.Error(codes.Unavailable, "claim locks not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeTrackerTenant(ctx, req.Username); err != nil {
		return nil, err
	}

	provider, conn, err := s.resolveWriterConn(ctx, req.Username, req.Connection)
	if err != nil {
		return nil, err
	}

	staleAfter := tracker.DefaultStaleAfter
	if req.StaleAfterSeconds > 0 {
		staleAfter = time.Duration(req.StaleAfterSeconds) * time.Second
	}

	// Serialize claims on this (username, connection, number) within
	// this daemon process — the cross-daemon race is handled by
