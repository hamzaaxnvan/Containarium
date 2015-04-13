package server

import (
	"context"
	"errors"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// resolveReaderConn resolves a named connection to its ReaderProvider
// adapter and a ready-to-use tracker.Conn (base URL, project, and the
// resolved broker credential) — the shared setup GetTrackerIssue,
// ListTrackerIssues, and GetTrackerChange all need before calling into
// an adapter.
//
// Tenant scoping and the tracker:read scope are the caller's
// responsibility (checked before this is called). This also enforces the
// run <-> connection JWT-claim binding (#1922 step 6): a run token bound
// to a different connection is rejected before the store is even
// consulted, so it can't distinguish "wrong connection" from "no such
// connection" for one it's not bound to.
func (s *ContainerServer) resolveReaderConn(ctx context.Context, username, connectionName string) (tracker.ReaderProvider, tracker.Conn, error) {
	if err := enforceConnectionBinding(ctx, connectionName); err != nil {
		return nil, tracker.Conn{}, err
	}
	trackerConn, err := s.trackerStore.Get(ctx, username, connectionName)
	if err != nil {
		return nil, tracker.Conn{}, mapTrackerError(err)
	}
	provider, err := s.trackerProviderFor(trackerConn.Provider)
	if err != nil {
		return nil, tracker.Conn{}, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	if s.secretsStore == nil {
		return nil, tracker.Conn{}, status.Error(codes.Unavailable, "secrets store not configured on this daemon")
	}
	cred, err := s.secretsStore.BrokerCredential(ctx, username, trackerConn.CredentialSecret)
	if err != nil {
		return nil, tracker.Conn{}, status.Errorf(codes.FailedPrecondition, "resolve broker credential: %v", err)
	}
	return provider, tracker.Conn{
		BaseURL:    trackerConn.BaseURL,
		Project:    trackerConn.Project,
		Credential: cred,
	}, nil
}

// GetTrackerIssue reads a single issue, including its comments.
func (s *ContainerServer) GetTrackerIssue(ctx context.Context, req *pb.GetTrackerIssueRequest) (*pb.GetTrackerIssueResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTrackerRead); err != nil {
		return nil, err
	}
	if s.trackerStore == nil {
		return nil, status.Error(codes.Unavailable, "tracker store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
