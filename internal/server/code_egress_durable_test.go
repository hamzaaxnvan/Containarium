package server

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Fail-closed guard for the coding-tool egress policy store (#2378): with
// Postgres configured but its store not in place, the in-memory store is an
// empty stand-in. Answering from it would report every tenant as
// unrestricted and lose admin writes at restart.

// startupServer is the server as NewDualServer leaves it when Postgres is
// configured: the in-memory stand-in, marked not durable.
func startupServer(t *testing.T) *CodingToolEgressPolicyServer {
	t.Helper()
	s, _ := newTestCodeEgressServer()
	s.SetDurable(false)
	return s
}

func assertCodeEgressUnavailable(t *testing.T, s *CodingToolEgressPolicyServer) {
	t.Helper()
	ctx := ceAdminCtx()
	enforce := pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE
	if _, err := s.SetCodingToolEgressPolicy(ctx, setReq("alice", enforce, []string{"192.0.2.0/24"}, nil)); codeOf(err) != codes.Unavailable {
		t.Errorf("Set = %v, want UNAVAILABLE", err)
	}
	for _, tenant := range []string{"alice", ""} {
		resp, err := s.GetCodingToolEgressPolicy(ctx, &pb.GetCodingToolEgressPolicyRequest{Tenant: tenant})
		if codeOf(err) != codes.Unavailable {
			t.Errorf("Get(%q) = %v (restricted=%v), want UNAVAILABLE, not an answer from the empty stand-in",
				tenant, err, resp.GetEffective().GetRestricted())
		}
	}
	if _, err := s.DeleteCodingToolEgressPolicy(ctx, &pb.DeleteCodingToolEgressPolicyRequest{Tenant: "alice"}); codeOf(err) != codes.Unavailable {
