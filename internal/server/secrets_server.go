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

	list, err := s.secretsStore.List(ctx, req.Username)
	if err != nil {
		return nil, mapSecretError(err)
	}

	out := make([]*pb.SecretMetadata, 0, len(list))
	for i := range list {
		out = append(out, toProtoSecretMetadata(&list[i]))
	}
	return &pb.ListSecretsResponse{Secrets: out}, nil
}

// DeleteSecret removes a single secret. Does NOT cascade-clean
// env-var stamps on running containers — callers invoke
// RefreshSecrets if they want the change to reach a running
// process.
func (s *ContainerServer) DeleteSecret(ctx context.Context, req *pb.DeleteSecretRequest) (*pb.DeleteSecretResponse, error) {
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

	if err := s.secretsStore.Delete(ctx, req.Username, req.Name); err != nil {
		return nil, mapSecretError(err)
	}

	log.Printf("[secrets] delete %s/%s", req.Username, req.Name)
	return &pb.DeleteSecretResponse{
		Message: fmt.Sprintf("secret %s deleted", req.Name),
	}, nil
}

// RefreshSecrets re-stamps the LXC's environment.<NAME> config
// keys from the current secret-store state for the tenant. Running
// processes keep their old env (POSIX semantics); new execs see
// the refreshed values.
func (s *ContainerServer) RefreshSecrets(ctx context.Context, req *pb.RefreshSecretsRequest) (*pb.RefreshSecretsResponse, error) {
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

	stamped, err := s.stampSecrets(ctx, req.Username)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "refresh secrets: %v", err)
	}

	log.Printf("[secrets] refresh %s: stamped=%d", req.Username, stamped)
	return &pb.RefreshSecretsResponse{
		Message: refreshSecretsMessage(s.boxes().Kind(), req.Username, stamped),
		Stamped: safecast.I32(stamped),
	}, nil
}

// SetTenantKMSKey sets or clears a tenant's per-tenant KEK for secrets
// envelope encryption, re-wrapping their existing secrets (#1630).
//
// Unlike every other RPC in this file, this does NOT use
// AuthorizeSecretTenant's self-access exception: admin role + the
// secrets:write scope granted EXPLICITLY on the token are required
// unconditionally, even when the caller's own subject happens to equal
// req.Username. Changing which key encrypts a tenant's secrets is a
// privileged operation performed on a tenant's behalf in this
// platform's actual usage (the cloud control plane, not the tenant
// itself) — see SetTenantKMSKeyRequest's proto comment.
func (s *ContainerServer) SetTenantKMSKey(ctx context.Context, req *pb.SetTenantKMSKeyRequest) (*pb.SetTenantKMSKeyResponse, error) {
	// Auth runs FIRST, before the store/argument checks below — an
	// unauthenticated or under-privileged caller must not be able to
	// learn whether this daemon even has a secrets store configured,
	// or probe argument validation (CodeRabbit finding on #1631).
	_, roles, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated subject in request context")
	}
	if !auth.HasRole(roles, auth.RoleAdmin) {
		return nil, status.Error(codes.PermissionDenied, "SetTenantKMSKey requires the admin role")
	}
	scopes, _ := auth.ScopesFromGRPCContext(ctx)
	if !auth.HasExplicitScope(scopes, auth.ScopeSecretsWrite) {
		return nil, status.Error(codes.PermissionDenied,
			"SetTenantKMSKey requires the "+auth.ScopeSecretsWrite+
				" scope to be granted explicitly on this token; the admin role alone does not imply it "+
				"(re-mint with `containarium token generate --scopes "+auth.ScopeSecretsWrite+" ...`)")
	}

	if s.secretsStore == nil {
		return nil, status.Error(codes.Unavailable, "secrets store not configured on this daemon")
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}

	if err := s.secretsStore.SetTenantKMSKey(ctx, req.Username, req.KekResourceName); err != nil {
		return nil, mapSecretError(err)
	}

	hasTenantKey := req.KekResourceName != ""
	msg := fmt.Sprintf("tenant %s's KMS key override cleared; new secrets use the shared KEK, existing ones keep their current key", req.Username)
	if hasTenantKey {
		msg = fmt.Sprintf("tenant %s now on its own KMS key", req.Username)
	}
	// Audit. Never log the key resource name's full path isn't
	// sensitive (GCP resource names aren't secrets), but keep the log
	// line's shape consistent with the other secrets RPCs regardless.
	log.Printf("[secrets] set-tenant-kms-key %s has_tenant_key=%v", req.Username, hasTenantKey)
	return &pb.SetTenantKMSKeyResponse{
		Message:      msg,
