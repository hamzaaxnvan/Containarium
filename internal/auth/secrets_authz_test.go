package auth

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// secretCtx builds an incoming gRPC context. Passing scopes="" omits the claim
// entirely, which is the shape `token generate` produces without --scopes and
// the one this whole change is about.
func secretCtx(username, roles, scopes string) context.Context {
	pairs := []string{MDKeyUsername, username, MDKeyRoles, roles}
	if scopes != "" {
		pairs = append(pairs, MDKeyScopes, scopes)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

// The composition this change exists to break: an unscoped admin token reading
// another tenant's secrets. Both of the old gates passed it — the scope check
// because the claim is absent, the tenant check because of the role.
func TestAuthorizeSecretTenant_UnscopedAdminDeniedCrossTenant(t *testing.T) {
	ctx := secretCtx("cloud-daemon", "admin", "")

	for _, scope := range []string{ScopeSecretsRead, ScopeSecretsWrite} {
		err := AuthorizeSecretTenant(ctx, "alice", scope)
		if err == nil {
