package threatdetect

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeSink is a FindingSink test double: records every Upsert call without
// touching Postgres, so engine registry/fan-out/panic-isolation behavior is
// unit-testable independent of the store's own dedupe SQL (that lives in
// store_integration_test.go / mem_store_test.go).
type fakeSink struct {
	mu      sync.Mutex
	upserts []*Finding
	err     error
}

func (s *fakeSink) Upsert(_ context.Context, f *Finding) (*Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	s.upserts = append(s.upserts, f)
	return f, nil
}

func (s *fakeSink) calls() []*Finding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Finding(nil), s.upserts...)
}

// reset clears recorded upserts, for a multi-phase test that reuses one
// engine/sink across sequential scenarios.
func (s *fakeSink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = nil
}

// testRule is a Rule test double whose behavior is supplied per-test via
// closures, so each test stays a short table-driven case instead of a new
// named type.
type testRule struct {
	id      pb.ThreatRuleId
	onFlows func(RuleContext, []netbpf.FlowRecord) []RawFinding
	onDeny  func(RuleContext, netbpf.DenyEvent) []RawFinding
	onSweep func(RuleContext, time.Time) []RawFinding
}

func (r *testRule) ID() pb.ThreatRuleId { return r.id }

func (r *testRule) OnFlows(ctx RuleContext, flows []netbpf.FlowRecord) []RawFinding {
	if r.onFlows == nil {
		return nil
	}
	return r.onFlows(ctx, flows)
}

func (r *testRule) OnDeny(ctx RuleContext, ev netbpf.DenyEvent) []RawFinding {
	if r.onDeny == nil {
		return nil
	}
	return r.onDeny(ctx, ev)
}

func (r *testRule) Sweep(ctx RuleContext, now time.Time) []RawFinding {
	if r.onSweep == nil {
		return nil
	}
	return r.onSweep(ctx, now)
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestEngine_OnFlows_FansOutToEveryRegisteredRule(t *testing.T) {
	sink := &fakeSink{}
	e := NewEngine(sink, "backend-1", false, nil, fixedClock(time.Unix(0, 0)))

	ruleA := &testRule{id: pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION, onFlows: func(RuleContext, []netbpf.FlowRecord) []RawFinding {
		return []RawFinding{{Rule: pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION, Severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH, TenantID: "t1", Subject: "203.0.113.9"}}
	}}
	ruleB := &testRule{id: pb.ThreatRuleId_THREAT_RULE_ID_CROSS_TENANT_FLOW, onFlows: func(RuleContext, []netbpf.FlowRecord) []RawFinding {
		return []RawFinding{{Rule: pb.ThreatRuleId_THREAT_RULE_ID_CROSS_TENANT_FLOW, Severity: pb.ThreatSeverity_THREAT_SEVERITY_CRITICAL, TenantID: "t1", Subject: "t2"}}
	}}
	e.Register(ruleA)
	e.Register(ruleB)

	e.OnFlows([]netbpf.FlowRecord{{Saddr: 1, Daddr: 2}})

	got := sink.calls()
	if len(got) != 2 {
		t.Fatalf("upserts = %d, want 2 (one per registered rule)", len(got))
	}
	byRule := map[pb.ThreatRuleId]*Finding{}
	for _, f := range got {
		byRule[f.Rule] = f
	}
	if f := byRule[pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION]; f == nil || f.BackendID != "backend-1" || f.Subject != "203.0.113.9" {
		t.Errorf("bad-destination finding = %+v, want backend-1/203.0.113.9", f)
	}
	if f := byRule[pb.ThreatRuleId_THREAT_RULE_ID_CROSS_TENANT_FLOW]; f == nil || f.Severity != pb.ThreatSeverity_THREAT_SEVERITY_CRITICAL {
		t.Errorf("cross-tenant finding = %+v, want CRITICAL", f)
	}
}

func TestEngine_OnDeny_FansOutToEveryRegisteredRule(t *testing.T) {
	sink := &fakeSink{}
	e := NewEngine(sink, "backend-1", false, nil, nil)

	rule := &testRule{id: pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST, onDeny: func(RuleContext, netbpf.DenyEvent) []RawFinding {
		return []RawFinding{{Rule: pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST, Severity: pb.ThreatSeverity_THREAT_SEVERITY_MEDIUM, TenantID: "t1", Subject: ""}}
	}}
	e.Register(rule)

	e.OnDeny(netbpf.DenyEvent{TenantID: 1})

	got := sink.calls()
	if len(got) != 1 || got[0].Rule != pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST {
		t.Fatalf("upserts = %+v, want 1 deny-burst finding", got)
