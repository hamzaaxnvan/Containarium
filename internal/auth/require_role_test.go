package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Phase 1.4 — RequireRole returns the right status code for the
// three input shapes: no subject, subject without role, subject
// with role. Tracks audit finding A-MED-4.

func TestRequireRole_NoSubject(t *testing.T) {
	err := RequireRole(context.Background(), RoleAdmin)
	if status.Code(err) != codes.Unauthenticated {
