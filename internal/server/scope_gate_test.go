package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Phase 1.7b — daemon-side scope enforcement on the hot-path
// handlers. Pairs with the MCP-side filter landed in PR #250.
//
// Each test below confirms that:
//   1. A scoped JWT that DOESN'T grant the required scope is
//      rejected with PermissionDenied — even when the role
//      and tenant checks would otherwise pass.
//   2. A scoped JWT that DOES grant the required scope clears
//      the scope gate (downstream nil-dep panics are fine —
//      they confirm the gate is structural, not coincidental).
//
// As with the Phase 1.4 RBAC tests, servers are constructed
// with nil deps; a passing test means the scope gate fires
// BEFORE any nil-deref.

func tenantWithScopes(name string, scopes ...string) context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(),
		name, []string{"user"}, scopes,
	)
}

// helper that wraps a call and asserts it does NOT return
// PermissionDenied. The handler may nil-deref after the gate
// passes; that's expected and proves the gate fired.
func mustPassScope(t *testing.T, label string, fn func() error) {
	t.Helper()
	defer func() {
		_ = recover()
	}()
