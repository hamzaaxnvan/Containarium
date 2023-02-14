package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #2415 — RequireExplicitScope: the gate for the audit-ingest write path.
// Unlike RequireScope it never falls through to the "no scopes claim =
// unrestricted" backwards-compat path, strict mode or not.

func TestScopeAuditIngest_IsKnown(t *testing.T) {
	if !IsKnownScope(ScopeAuditIngest) {
		t.Fatalf("%q must be registered in AllScopes", ScopeAuditIngest)
	}
	if ScopeAuditIngest != "audit:ingest" {
		t.Fatalf("scope string = %q, want audit:ingest", ScopeAuditIngest)
	}
}

func TestRequireExplicitScope(t *testing.T) {
	SetStrictScopes(false)
	t.Cleanup(func() { SetStrictScopes(false) })

	tests := []struct {
		name   string
		ctx    context.Context
		strict bool
		want   codes.Code
	}{
		{
			name: "no subject",
			ctx:  context.Background(),
			want: codes.Unauthenticated,
		},
		{
			name: "scope granted",
			ctx:  ContextWithTestSubjectScopes(context.Background(), "shipper", []string{"service"}, []string{ScopeAuditIngest}),
			want: codes.OK,
