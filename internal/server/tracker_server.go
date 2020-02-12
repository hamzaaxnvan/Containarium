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
		Connection: toProtoTrackerConnection(conn),
	}, nil
}

// GetTrackerConnection returns a single named connection's metadata.
// Never returns the credential value — TrackerConnection only ever
// carries the secret's name.
func (s *ContainerServer) GetTrackerConnection(ctx context.Context, req *pb.GetTrackerConnectionRequest) (*pb.GetTrackerConnectionResponse, error) {
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

	conn, err := s.trackerStore.Get(ctx, req.Username, req.Name)
	if err != nil {
		return nil, mapTrackerError(err)
	}
	return &pb.GetTrackerConnectionResponse{Connection: toProtoTrackerConnection(conn)}, nil
}

// ListTrackerConnections returns every connection a tenant owns.
func (s *ContainerServer) ListTrackerConnections(ctx context.Context, req *pb.ListTrackerConnectionsRequest) (*pb.ListTrackerConnectionsResponse, error) {
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

	list, err := s.trackerStore.List(ctx, req.Username)
	if err != nil {
		return nil, mapTrackerError(err)
	}
	out := make([]*pb.TrackerConnection, 0, len(list))
	for i := range list {
		out = append(out, toProtoTrackerConnection(&list[i]))
	}
	return &pb.ListTrackerConnectionsResponse{Connections: out}, nil
}

// DeleteTrackerConnection removes a named connection. Does not delete
// the underlying credential secret.
func (s *ContainerServer) DeleteTrackerConnection(ctx context.Context, req *pb.DeleteTrackerConnectionRequest) (*pb.DeleteTrackerConnectionResponse, error) {
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

	// Best-effort lookup purely to enrich the audit detail with which
	// provider/project this connection pointed at — a failure here must
	// never block the delete itself, so the error is discarded.
	existing, _ := s.trackerStore.Get(ctx, req.Username, req.Name)

	if err := s.trackerStore.Delete(ctx, req.Username, req.Name); err != nil {
		return nil, mapTrackerError(err)
	}
	log.Printf("[tracker] connection deleted %s/%s", req.Username, req.Name)
	detail := trackerConnectionAuditDetail{}
	if existing != nil {
		detail.Provider = existing.Provider.String()
		detail.BaseURL = existing.BaseURL
		detail.Project = existing.Project
		detail.CredentialSecret = existing.CredentialSecret
	}
	s.auditTrackerConnectionWrite(ctx, "tracker.connection_deleted", req.Username, req.Name, detail)
	return &pb.DeleteTrackerConnectionResponse{
		Message: fmt.Sprintf("connection %s deleted", req.Name),
	}, nil
}

// GetTrackerStatus probes a connection's credential live, against the
// tracker itself — reachability, validity, scopes, expiry, and breadth
// versus the provider's preferred credential type.
func (s *ContainerServer) GetTrackerStatus(ctx context.Context, req *pb.GetTrackerStatusRequest) (*pb.GetTrackerStatusResponse, error) {
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

	conn, err := s.trackerStore.Get(ctx, req.Username, req.Name)
	if err != nil {
		return nil, mapTrackerError(err)
	}
	resp := &pb.GetTrackerStatusResponse{Connection: toProtoTrackerConnection(conn)}

	// Host git preflight (#1923, decision D2) — daemon-wide, not
	// connection-specific, so it's set unconditionally here rather than
	// gated behind the credential checks below: an operator should see
	// "SubmitTrackerChange will fail for every connection on this
	// daemon" even when the connection being checked has an unrelated
	// problem (or none at all).
	gitVersion, gitErr := submit.CheckHostGit()
	resp.HostGitVersion = gitVersion
	resp.HostGitSufficient = gitErr == nil

	describer, derr := s.trackerProviderFor(conn.Provider)
	if derr != nil {
		resp.Detail = derr.Error()
		return resp, nil
	}
	if s.secretsStore == nil {
		resp.Detail = "secrets store not configured on this daemon"
		return resp, nil
	}
	cred, cerr := s.secretsStore.BrokerCredential(ctx, conn.Username, conn.CredentialSecret)
	if cerr != nil {
		resp.Detail = fmt.Sprintf("resolve broker credential: %v", cerr)
		return resp, nil
	}

	info, err := describer.DescribeCredential(ctx, tracker.Conn{
		BaseURL: conn.BaseURL, Project: conn.Project, Credential: cred,
	})
	switch {
	case err == nil:
		resp.Reachable = true
		resp.CredentialValid = true
		resp.CredentialBreadth = breadthToProto(info.Breadth)
		resp.CredentialKind = kindToProto(info.Kind)
		resp.CredentialScopes = info.Scopes
		if !info.ExpiresAt.IsZero() {
			resp.CredentialExpiresAt = timestamppb.New(info.ExpiresAt)
		}
		// Keep the stored expiry in sync with what was just observed —
		// the same best-effort persistence SetTrackerConnection does.
		if serr := s.trackerStore.SetCredentialExpiry(ctx, conn.Username, conn.Name, info.ExpiresAt); serr != nil {
			log.Printf("[tracker] %s/%s: persist credential expiry: %v", conn.Username, conn.Name, serr)
		} else if resp.Connection != nil {
			resp.Connection.CredentialExpiresAt = resp.CredentialExpiresAt
		}
	case errors.Is(err, tracker.ErrCredentialInvalid):
		resp.Reachable = true
		resp.CredentialValid = false
		resp.Detail = err.Error()
	case errors.Is(err, tracker.ErrUnreachable):
		resp.Detail = err.Error()
	default:
		resp.Detail = err.Error()
	}
	return resp, nil
}

// describeCredentialBestEffort probes conn's own credential and, on
// success, persists its expiry onto conn (in-memory) and the store.
//
// Best-effort by design: SetTrackerConnection's whole point is
// registering a connection's METADATA (provider, project, which secret
// to use), and a describe failure here — the tracker briefly
// unreachable, a token not yet propagated upstream, a self-managed
// instance reachable only from a network the daemon doesn't have a
