package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/pkg/core/container"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetSecretsStore wires the tenant-secrets backend onto the server.
// Called from dual_server.go after the Postgres connection + master
// key have been resolved. Nil store keeps the SecretsService RPCs
// returning Unavailable (--standalone daemons).
func (s *ContainerServer) SetSecretsStore(store *secrets.Store) {
	s.secretsStore = store
}

// SetSecret creates or updates a tenant secret. Idempotent —
// repeated calls with the same (username, name) bump the version
// and replace the value. Admin JWT required (design decision #3).
func (s *ContainerServer) SetSecret(ctx context.Context, req *pb.SetSecretRequest) (*pb.SetSecretResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecretsWrite); err != nil {
		return nil, err
	}
	if s.secretsStore == nil {
		return nil, status.Error(codes.Unavailable, "secrets store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeSecretTenant(ctx, req.Username, auth.ScopeSecretsWrite); err != nil {
		return nil, err
	}

	delivery, err := resolveDelivery(req.DeliveryMode, req.Delivery) //nolint:staticcheck // req.Delivery is deprecated but still accepted
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	meta, err := s.secretsStore.Set(ctx, req.Username, req.Name, req.Value, delivery)
	if err != nil {
		return nil, mapSecretError(err)
	}

	// Audit. Never log the value.
	log.Printf("[secrets] set %s/%s version=%d delivery=%s", req.Username, req.Name, meta.Version, meta.Delivery)

	msg := "secret created"
	if meta.Version > 1 {
		msg = fmt.Sprintf("secret updated to version %d", meta.Version)
	}
	return &pb.SetSecretResponse{
		Message: msg,
		Secret:  toProtoSecretMetadata(meta),
	}, nil
}

// GetSecret returns the decrypted plaintext value. Always
// audit-logged — every gRPC call on this server produces an
// audit_logs row via audit.GRPCInterceptor (dual_server.go), regardless
// of transport (native gRPC or REST through grpc-gateway); the
// log.Printf below is a separate, non-persistent operational log line,
// not the audit mechanism itself (#1605). The agent / operator sees
// what they wrote (decision #6); v2 layers a per-secret read_via_api
// flag for write-only rotation.
func (s *ContainerServer) GetSecret(ctx context.Context, req *pb.GetSecretRequest) (*pb.GetSecretResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecretsRead); err != nil {
		return nil, err
	}
	if s.secretsStore == nil {
		return nil, status.Error(codes.Unavailable, "secrets store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeSecretTenant(ctx, req.Username, auth.ScopeSecretsRead); err != nil {
		return nil, err
	}

	meta, value, err := s.secretsStore.Get(ctx, req.Username, req.Name)
	if err != nil {
		if errors.Is(err, secrets.ErrBrokerOnly) {
			// Write-only by design: the metadata is real, the value
			// isn't returned regardless of the caller's scopes.
			log.Printf("[secrets] get %s/%s version=%d delivery=broker (write-only, no value returned)",
				req.Username, req.Name, meta.Version)
			return &pb.GetSecretResponse{Secret: toProtoSecretMetadata(meta)}, nil
		}
		return nil, mapSecretError(err)
	}

	log.Printf("[secrets] get %s/%s version=%d", req.Username, req.Name, meta.Version)
	return &pb.GetSecretResponse{
		Secret: toProtoSecretMetadata(meta),
		Value:  value,
	}, nil
}

// ListSecrets returns metadata for every secret owned by the
// tenant. Values are never returned by this path.
func (s *ContainerServer) ListSecrets(ctx context.Context, req *pb.ListSecretsRequest) (*pb.ListSecretsResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecretsRead); err != nil {
		return nil, err
	}
	if s.secretsStore == nil {
		return nil, status.Error(codes.Unavailable, "secrets store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeSecretTenant(ctx, req.Username, auth.ScopeSecretsRead); err != nil {
		return nil, err
	}

