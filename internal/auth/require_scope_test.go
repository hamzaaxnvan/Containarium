package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Phase 1.7b — RequireScope semantics.

func TestRequireScope_AllowsWhenNoSubject_ReturnsUnauthenticated(t *testing.T) {
	err := RequireScope(context.Background(), ScopeSecretsWrite)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v want Unauthenticated", err)
	}
}

func TestRequireScope_AllowsWhenScopesAbsent(t *testing.T) {
	// Pre-1.7 token (no scopes claim) — must keep working
	// for backwards compat. The role check is still the
	// authoritative gate for these tokens.
	ctx := ContextWithTestSubject(context.Background(), "alice", "user")
	if err := RequireScope(ctx, ScopeSecretsWrite); err != nil {
		t.Fatalf("absent scopes claim should pass (backwards compat); got %v", err)
	}
}

func TestRequireScope_RejectsWhenScopeMissing(t *testing.T) {
	ctx := ContextWithTestSubjectScopes(context.Background(),
		"alice", []string{"user"}, []string{ScopeContainersRead},
