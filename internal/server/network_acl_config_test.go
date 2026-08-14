package server

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2348: UpdateContainerACL used to build the custom rule set and then throw
// it away (EnsureACLForContainer was called with a preset), and mapped
// CUSTOM/UNSPECIFIED to full-isolation silently. #2359: ingress is owned by
// the tenant network guard, so the per-container ACL carries egress only.
// The config an update writes is a pure function of the request.
func TestACLConfigFromRequest(t *testing.T) {
	const proxyIP, bridge = "10.100.0.2", "10.100.0.0/24"

	tests := []struct {
		name     string
		req      *pb.UpdateContainerACLRequest
		wantCode codes.Code // codes.OK means no error
		check    func(t *testing.T, cfg incus.ACLConfig)
	}{
		{
			name: "custom egress rules are applied verbatim",
			req: &pb.UpdateContainerACLRequest{
				Username: "alice",
				Preset:   pb.ACLPreset_ACL_PRESET_CUSTOM,
				EgressRules: []*pb.ACLRule{{
