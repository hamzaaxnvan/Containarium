package threatdetect

import (
	"context"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func testRuleContext(tenantByIP map[string]string) RuleContext {
	return RuleContext{
		TenantForIP: func(ip netip.Addr) (string, bool) {
			t, ok := tenantByIP[ip.String()]
			return t, ok
		},
	}
}

func flow(src, dst string) netbpf.FlowRecord {
	sAddr := netip.MustParseAddr(src).As4()
	dAddr := netip.MustParseAddr(dst).As4()
	return netbpf.FlowRecord{
		Saddr:   beUint32(sAddr),
		Daddr:   beUint32(dAddr),
		Sport:   54321,
		Dport:   443,
		Proto:   6, // tcp
		Packets: 42,
		Bytes:   4096,
	}
}

func TestBadDestinationRule_ExactIPHit(t *testing.T) {
	r := newBadDestinationRuleForTest(t, []BadDestinationEntry{
		{CIDR: "198.51.100.7/32", Label: "known miner", Source: "baseline"},
	}, nil)
	ctx := testRuleContext(map[string]string{"10.0.0.5": "tenant-a"})

	findings := r.OnFlows(ctx, []netbpf.FlowRecord{flow("10.0.0.5", "198.51.100.7")})

	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.Rule != pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION {
		t.Errorf("Rule = %v, want BAD_DESTINATION", f.Rule)
	}
	if f.Severity != pb.ThreatSeverity_THREAT_SEVERITY_HIGH {
		t.Errorf("Severity = %v, want HIGH", f.Severity)
	}
	if f.TenantID != "tenant-a" {
		t.Errorf("TenantID = %q, want tenant-a", f.TenantID)
	}
	if f.Subject != "198.51.100.7" {
		t.Errorf("Subject = %q, want the destination IP", f.Subject)
	}
	if len(f.Evidence.Flows) != 1 || f.Evidence.Flows[0].DstIP != "198.51.100.7" {
		t.Errorf("Evidence.Flows = %+v, want one flow to the bad destination", f.Evidence.Flows)
	}
}

func TestBadDestinationRule_CIDRHit(t *testing.T) {
	r := newBadDestinationRuleForTest(t, []BadDestinationEntry{
		{CIDR: "198.51.100.0/24", Label: "known mining range", Source: "baseline"},
	}, nil)
	ctx := testRuleContext(map[string]string{"10.0.0.5": "tenant-a"})

	findings := r.OnFlows(ctx, []netbpf.FlowRecord{flow("10.0.0.5", "198.51.100.200")})

	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1 (CIDR should match)", len(findings))
	}
}

func TestBadDestinationRule_Miss(t *testing.T) {
	r := newBadDestinationRuleForTest(t, []BadDestinationEntry{
		{CIDR: "198.51.100.0/24", Label: "known mining range", Source: "baseline"},
	}, nil)
	ctx := testRuleContext(map[string]string{"10.0.0.5": "tenant-a"})

	findings := r.OnFlows(ctx, []netbpf.FlowRecord{flow("10.0.0.5", "8.8.8.8")})

	if len(findings) != 0 {
		t.Fatalf("got %d findings, want 0 for a destination not on the list", len(findings))
	}
}

func TestBadDestinationRule_UnknownSourceIP_StillFiresWithEmptyTenant(t *testing.T) {
	// Design doc: a rule must never guess at an unknown IP's tenant — but an
	// unresolved source tenant shouldn't suppress a HIGH finding about a
	// destination that IS known-bad; the operator can still see the flow.
	r := newBadDestinationRuleForTest(t, []BadDestinationEntry{
		{CIDR: "198.51.100.7/32", Label: "known miner", Source: "baseline"},
	}, nil)
	ctx := testRuleContext(nil) // no IP resolves

	findings := r.OnFlows(ctx, []netbpf.FlowRecord{flow("10.0.0.5", "198.51.100.7")})

	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].TenantID != "" {
		t.Errorf("TenantID = %q, want empty for an unresolved source IP", findings[0].TenantID)
	}
}

func TestBadDestinationRule_OperatorAddedEntry(t *testing.T) {
	r := newBadDestinationRuleForTest(t, nil, nil)
	ctx := testRuleContext(map[string]string{"10.0.0.5": "tenant-a"})

	// Not on the (empty) baseline list yet.
	if findings := r.OnFlows(ctx, []netbpf.FlowRecord{flow("10.0.0.5", "192.0.2.55")}); len(findings) != 0 {
		t.Fatalf("got %d findings before adding the entry, want 0", len(findings))
	}

	if _, err := r.AddDestination(context.Background(), "192.0.2.55/32", "operator-flagged host"); err != nil {
		t.Fatalf("AddDestination: %v", err)
	}

	findings := r.OnFlows(ctx, []netbpf.FlowRecord{flow("10.0.0.5", "192.0.2.55")})
	if len(findings) != 1 {
		t.Fatalf("got %d findings after adding the entry, want 1", len(findings))
	}
}

func TestBadDestinationRule_RemoveOperatorEntry(t *testing.T) {
	r := newBadDestinationRuleForTest(t, nil, nil)
	ctx := testRuleContext(map[string]string{"10.0.0.5": "tenant-a"})

	if _, err := r.AddDestination(context.Background(), "192.0.2.55/32", "operator-flagged host"); err != nil {
		t.Fatalf("AddDestination: %v", err)
	}
	if err := r.RemoveDestination(context.Background(), "192.0.2.55/32"); err != nil {
		t.Fatalf("RemoveDestination: %v", err)
	}

