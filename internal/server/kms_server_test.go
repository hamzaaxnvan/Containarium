package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// kmsAdminCtx builds a context carrying the kms:admin scope + admin role.
func kmsAdminCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(),
		"ops", []string{auth.RoleAdmin}, []string{auth.ScopeKMSAdmin})
}

func TestSetKMSStatus_NormalizesBackend(t *testing.T) {
	cs := &ContainerServer{}
	cs.SetKMSStatus("  AWS  ", "aws kms (region=us-west-2)", true, false)
	if cs.kmsBackend != "aws" {
		t.Fatalf("backend not normalized: %q", cs.kmsBackend)
	}
	// Empty backend defaults to "none".
	cs.SetKMSStatus("", "disabled", false, false)
	if cs.kmsBackend != "none" {
