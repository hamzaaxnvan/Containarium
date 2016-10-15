package netbpf

import (
	"net/netip"
	"testing"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestCompileDeny(t *testing.T) {
	c := mustCompile(t, &pb.NetworkPolicy{
		Tenant: "alice",
		DenyRules: []*pb.NetworkPolicyDenyRule{
			{Cidr: "1.2.3.4", Port: 6379, Proto: "tcp"}, // host → /32, tcp/6379
			{Cidr: "10.0.0.0/8"},                        // any port/proto
