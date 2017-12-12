package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/internal/tracker"
	trackergithub "github.com/footprintai/containarium/internal/tracker/github"
	trackergitlab "github.com/footprintai/containarium/internal/tracker/gitlab"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// trackerConnectionAuditDetail is the payload for a connection-level
// audit event (create / update / rotate / delete). CredentialSecret is
// the SECRET'S NAME in the secrets store, never its value — the same
// reference the connection record itself carries.
type trackerConnectionAuditDetail struct {
	Provider         string `json:"provider,omitempty"`
	BaseURL          string `json:"base_url,omitempty"`
	Project          string `json:"project,omitempty"`
	CredentialSecret string `json:"credential_secret,omitempty"`
}

// auditTrackerConnectionWrite records a connection CRUD outcome —
// connect, disconnect, and credential rotation (an update that changes
// CredentialSecret) are all audit events per #1921's acceptance
// criteria. Best-effort, same convention as auditTrackerWrite: audit
// must never fail the call, and this is a no-op until the audit store
// is wired.
func (s *ContainerServer) auditTrackerConnectionWrite(ctx context.Context, action, username, connection string, detail trackerConnectionAuditDetail) {
	if s.auditStore == nil {
		return
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		log.Printf("[tracker] marshal audit detail for %s: %v", action, err)
		return
	}
	if err := s.auditStore.Log(ctx, &audit.AuditEntry{
		Username:     username,
		Action:       action,
		ResourceType: "tracker_connection",
		ResourceID:   fmt.Sprintf("%s/%s", username, connection),
		Detail:       string(payload),
	}); err != nil {
		log.Printf("[tracker] audit %s %s/%s: %v", action, username, connection, err)
	}
}

// SetTrackerStore wires the tracker-connections backend onto the server.
// Called from dual_server.go after the Postgres connection has been
// resolved. Nil store keeps the TrackerService RPCs returning
// Unavailable (--standalone daemons), same convention as
// SetSecretsStore.
func (s *ContainerServer) SetTrackerStore(store *tracker.Store) {
	s.trackerStore = store
}

// SetRunRegistry wires the shared in-memory run registry (#1922) — the
// SAME instance dual_server.go gives to AgentSkillServer, so a run's
// skill/model (for the identity stamp) and liveness (for
// ClaimTrackerIssue) can be resolved without a database round trip.
func (s *ContainerServer) SetRunRegistry(r *runlease.Registry) {
	s.runRegistry = r
}

// SetClaimLocks wires the shared ClaimLocks used to serialize
// ClaimTrackerIssue calls within this daemon process.
func (s *ContainerServer) SetClaimLocks(l *tracker.ClaimLocks) {
	s.claimLocks = l
}

// SetTrackerDescribers overrides the provider -> ReaderProvider
// registry. Production wiring never calls this — trackerProviderFor
// constructs the real GitHub/GitLab adapters per call when the field is
// nil, the same "cheap to construct, no shared mutable state" idiom as
// boxes() falling back to boxlxc.New(s.manager). Tests use this to
// substitute fakes.
//
// Named for #1921 (DescribeCredential only); the parameter type widened
// to tracker.ReaderProvider in #1922 when GetIssue/ListIssues/GetChange
// needed the same per-provider resolution — every existing caller only
// used DescribeCredential and keeps compiling unchanged, since
// ReaderProvider embeds CredentialDescriber.
func (s *ContainerServer) SetTrackerDescribers(m map[pb.TrackerProvider]tracker.ReaderProvider) {
	s.trackerDescribers = m
}

// trackerProviderFor resolves the ReaderProvider for provider.
func (s *ContainerServer) trackerProviderFor(provider pb.TrackerProvider) (tracker.ReaderProvider, error) {
	if s.trackerDescribers != nil {
		d, ok := s.trackerDescribers[provider]
		if !ok {
			return nil, fmt.Errorf("no reader provider registered for provider %v", provider)
		}
		return d, nil
	}
	switch provider {
	case pb.TrackerProvider_TRACKER_PROVIDER_GITHUB:
		return trackergithub.New(nil), nil
	case pb.TrackerProvider_TRACKER_PROVIDER_GITLAB:
		return trackergitlab.New(nil), nil
	default:
		return nil, fmt.Errorf("no reader provider for provider %v", provider)
	}
}

// SetTrackerConnection creates or updates a tracker connection.
// Connection CRUD is gated by tracker:admin (NOT tracker:read/write,
// which are what an agent's run-scoped JWT carries for the tracker
// verbs themselves, #1922) — naming where a tracker is and which
// credential to trust is an operator decision.
func (s *ContainerServer) SetTrackerConnection(ctx context.Context, req *pb.SetTrackerConnectionRequest) (*pb.SetTrackerConnectionResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTrackerAdmin); err != nil {
		return nil, err
	}
	if s.trackerStore == nil {
		return nil, status.Error(codes.Unavailable, "tracker store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeTenant(ctx, req.Username); err != nil {
		return nil, err
	}

	if err := s.requireBrokerOnlySecret(ctx, req.Username, req.CredentialSecret); err != nil {
		return nil, err
	}
	if err := validateTrackerPolicy(req.Policy); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	conn, err := s.trackerStore.Set(ctx, tracker.Connection{
		Username:         req.Username,
		Name:             req.Name,
		Provider:         req.Provider,
		BaseURL:          req.BaseUrl,
		Project:          req.Project,
		CredentialSecret: req.CredentialSecret,
		Policy:           req.Policy,
	})
	if err != nil {
		return nil, mapTrackerError(err)
	}

	// Best-effort: an operator may be registering a connection before
	// the tracker is reachable from the daemon's network, or before the
	// token is fully propagated upstream. A describe failure here must
	// never fail the connection write itself — see
	// describeCredentialBestEffort's own doc comment.
	s.describeCredentialBestEffort(ctx, conn)

	log.Printf("[tracker] connection set %s/%s provider=%s project=%s", req.Username, req.Name, req.Provider, req.Project)

	msg := "connection created"
	action := "tracker.connection_created"
	if !conn.CreatedAt.Equal(conn.UpdatedAt) {
		msg = "connection updated"
		action = "tracker.connection_updated"
	}
	s.auditTrackerConnectionWrite(ctx, action, req.Username, req.Name, trackerConnectionAuditDetail{
		Provider:         req.Provider.String(),
		BaseURL:          req.BaseUrl,
		Project:          req.Project,
		CredentialSecret: req.CredentialSecret,
	})
	return &pb.SetTrackerConnectionResponse{
		Message:    msg,
