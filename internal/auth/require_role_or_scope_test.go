package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #621 — RequireRoleOrScope passes if the caller holds the role OR
// explicitly holds the scope. It gates read paths an admin reaches by
// role and a least-privilege token reaches by an explicit read scope.
// Unlike RequireScope, a nil/absent scopes claim must NOT unlock the read
// (that's HasExplicitScope, not HasScope).

func TestRequireRoleOrScope_NoSubject(t *testing.T) {
	err := RequireRoleOrScope(context.Background(), RoleAdmin, ScopeTokensRead)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v (%v)", status.Code(err), err)
	}
}

func TestRequireRoleOrScope_AdminByRole(t *testing.T) {
	// Admin role, an unrelated scope — the role alone passes.
	ctx := ContextWithTestSubjectScopes(context.Background(),
		"ops", []string{RoleAdmin}, []string{ScopeContainersRead})
	if err := RequireRoleOrScope(ctx, RoleAdmin, ScopeTokensRead); err != nil {
		t.Fatalf("admin by role should pass: %v", err)
	}
}

func TestRequireRoleOrScope_NonAdminWithScope(t *testing.T) {
	ctx := ContextWithTestSubjectScopes(context.Background(),
