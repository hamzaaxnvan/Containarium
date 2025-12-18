package netpolicy

import (
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestCompile_DenyRules_OK(t *testing.T) {
	got, err := Compile(&pb.NetworkPolicy{
		Tenant: "alice",
		DenyRules: []*pb.NetworkPolicyDenyRule{
			{Cidr: "1.2.3.4", Port: 6379, Proto: "TCP", Note: "CVE-2024-0001"}, // bare host → /32, proto upper-cased
			{Cidr: "10.0.0.0/8"}, // CIDR, any port/proto
			{Cidr: "1.2.3.4", Port: 6379, Proto: "tcp", Note: "dup-key-wins"}, // same CIDR → dedup (last wins)
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// 3 input rules, two share the CIDR 1.2.3.4/32 → 2 unique.
	if len(got.DenyRules) != 2 {
		t.Fatalf("want 2 deny rules, got %d: %+v", len(got.DenyRules), got.DenyRules)
	}
	// Sorted by CIDR string: "1.2.3.4/32" < "10.0.0.0/8".
	r0, r1 := got.DenyRules[0], got.DenyRules[1]
	if r0.CIDR.String() != "1.2.3.4/32" {
		t.Errorf("rule0 cidr = %s, want 1.2.3.4/32", r0.CIDR)
	}
	if r0.Port != 6379 || r0.Proto != 6 {
		t.Errorf("rule0 port/proto = %d/%d, want 6379/6", r0.Port, r0.Proto)
	}
	if r0.Note != "dup-key-wins" {
		t.Errorf("rule0 note = %q, want the later duplicate's note", r0.Note)
	}
	if r1.CIDR.String() != "10.0.0.0/8" || r1.Port != 0 || r1.Proto != 0 {
		t.Errorf("rule1 = %+v, want 10.0.0.0/8 any/any", r1)
	}
}

func TestCompile_DenyRules_DedupByCIDR(t *testing.T) {
	// Two rules for the SAME CIDR with DIFFERENT ports collapse to one (the last
