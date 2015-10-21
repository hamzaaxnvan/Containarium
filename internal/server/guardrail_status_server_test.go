package server

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestGetGuardrailStatus_ReportsEachEffectivePolicy (#2737): a fresh daemon
// reports BASELINE; a policy with an inbound BLOCK rule reports ADMIN; a
// policy without one reports NONE. Any authenticated caller may read it.
func TestGetGuardrailStatus_ReportsEachEffectivePolicy(t *testing.T) {
	s, _ := newGPServer()
	get := func(ctx context.Context) *pb.GetGuardrailStatusResponse {
		t.Helper()
		resp, err := s.GetGuardrailStatus(ctx, &pb.GetGuardrailStatusRequest{})
		if err != nil {
			t.Fatalf("GetGuardrailStatus: %v", err)
		}
		return resp
	}

