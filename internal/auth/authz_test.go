package auth

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestHasRole(t *testing.T) {
	if !HasRole([]string{"user", "admin"}, "admin") {
		t.Fatal("admin should be found")
	}
	if HasRole([]string{"user"}, "admin") {
		t.Fatal("admin should NOT be found")
	}
	if HasRole(nil, "admin") {
		t.Fatal("nil roles should not match")
	}
}

func TestSubjectFromGRPCContext_Metadata(t *testing.T) {
	md := metadata.Pairs(MDKeyUsername, "alice", MDKeyRoles, "user,viewer")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	u, roles, ok := SubjectFromGRPCContext(ctx)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if u != "alice" {
		t.Fatalf("username: got %q, want alice", u)
	}
	if len(roles) != 2 || roles[0] != "user" || roles[1] != "viewer" {
		t.Fatalf("roles: got %v, want [user viewer]", roles)
	}
}

func TestSubjectFromGRPCContext_ContextFallback(t *testing.T) {
	claims := &Claims{Username: "bob", Roles: []string{"admin"}}
	ctx := ContextWithClaims(context.Background(), claims)

	u, roles, ok := SubjectFromGRPCContext(ctx)
	if !ok || u != "bob" {
		t.Fatalf("got u=%q ok=%v, want bob/true", u, ok)
	}
	if !HasRole(roles, "admin") {
		t.Fatalf("expected admin role, got %v", roles)
	}
}

func TestSubjectFromGRPCContext_None(t *testing.T) {
	_, _, ok := SubjectFromGRPCContext(context.Background())
	if ok {
		t.Fatal("expected ok=false for empty context")
	}
}
