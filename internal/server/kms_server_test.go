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
		t.Fatalf("empty backend should normalize to none; got %q", cs.kmsBackend)
	}
}

func TestGetKMSStatus_ReturnsSnapshot(t *testing.T) {
	cs := &ContainerServer{}
	cs.SetKMSStatus("vault", "vault transit (addr=https://v|transit|k)", true, true)
	srv := NewKmsServer(cs)

	resp, err := srv.GetKMSStatus(kmsAdminCtx(), &pb.GetKMSStatusRequest{})
	if err != nil {
		t.Fatalf("GetKMSStatus: %v", err)
	}
	if resp.Backend != "vault" || !resp.KmsConfigured || !resp.RequireEnvelope {
		t.Fatalf("unexpected status: %+v", resp)
	}
	if resp.Description == "" {
		t.Fatal("description should pass through")
	}
}

func TestKMS_RequiresAdminScope(t *testing.T) {
	srv := NewKmsServer(&ContainerServer{})

	// Missing the kms:admin scope (has a different scope).
	noScope := auth.ContextWithTestSubjectScopes(context.Background(),
		"ops", []string{auth.RoleAdmin}, []string{auth.ScopeSecretsRead})
	if _, err := srv.GetKMSStatus(noScope, &pb.GetKMSStatusRequest{}); status.Code(err) != codes.PermissionDenied {
