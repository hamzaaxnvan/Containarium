package server

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeHealthStore is an in-memory finding store with the same open-dedupe
// contract as FindingStore/MemFindingStore: an Upsert whose (rule, tenant,
// subject) matches an open row bumps that row's count instead of inserting.
type fakeHealthStore struct {
	mu      sync.Mutex
	nextID  int64
	rows    []*threatdetect.Finding
	upserts int
	clock   int64 // monotonic stand-in for LastSeen, so List can order newest-first
}

func (s *fakeHealthStore) Upsert(_ context.Context, f *threatdetect.Finding) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts++
	for _, r := range s.rows {
		if r.State == threatdetect.FindingStateOpen && r.Rule == f.Rule && r.TenantID == f.TenantID && r.Subject == f.Subject {
			r.Count++
			s.clock++
			r.LastSeen = time.Unix(s.clock, 0)
			return r, nil
		}
	}
	s.nextID++
	s.clock++
	cp := *f
	cp.LastSeen = time.Unix(s.clock, 0)
	cp.ID = s.nextID
	cp.State = threatdetect.FindingStateOpen
	cp.Count = 1
	s.rows = append(s.rows, &cp)
	return &cp, nil
}

func (s *fakeHealthStore) Get(_ context.Context, id int64) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, threatdetect.ErrFindingNotFound
}

func (s *fakeHealthStore) List(_ context.Context, filter threatdetect.ListFilter) ([]*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Same filter, order and page cap as FindingStore/MemFindingStore.List.
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []*threatdetect.Finding
	for _, r := range s.rows {
		if filter.State != "" && r.State != filter.State {
			continue
		}
		if filter.Rule != pb.ThreatRuleId_THREAT_RULE_ID_UNSPECIFIED && r.Rule != filter.Rule {
			continue
		}
		if filter.BackendID != "" && r.BackendID != filter.BackendID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeHealthStore) Resolve(_ context.Context, id int64) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.ID == id {
			if r.State != threatdetect.FindingStateOpen {
				return nil, threatdetect.ErrFindingNotOpen
			}
			r.State = threatdetect.FindingStateResolved
			return r, nil
		}
	}
	return nil, threatdetect.ErrFindingNotFound
}

func (s *fakeHealthStore) open() []*threatdetect.Finding {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*threatdetect.Finding
	for _, r := range s.rows {
		if r.State == threatdetect.FindingStateOpen {
			out = append(out, r)
		}
	}
	return out
}

func statusResp(state pb.SentryState, reason string) *pb.GetSentryStatusResponse {
	return &pb.GetSentryStatusResponse{State: state, Reason: reason}
}

// TestSentryHealthCheck drives the loop's single-tick Check through a
// sequence of sentry states and asserts the open SENTRY_UNAVAILABLE rows
// after the last one (#2634 acceptance criteria 1 and 2).
func TestSentryHealthCheck(t *testing.T) {
	const (
		noObj   = "eBPF object not loaded (set CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT)"
		failed  = "eBPF network-policy enforcer failed to start: load: permission denied"
		degrade = "FindingStore has no Postgres connection; findings are not persisted across a restart"

		responderDown = "DEGRADED (security_actions store unavailable): responses disabled"
	)
	unhealthyRule := &pb.GetSentryStatusResponse{
		State:  pb.SentryState_SENTRY_STATE_OK,
		Reason: "rule DENY_BURST unhealthy: panic: boom",
		Rules:  []*pb.RuleStatus{{Rule: pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST, Healthy: false, LastError: "panic: boom"}},
	}
	// The same rule panicking again with different text (an index, an
	// address): the subject must not change, or every tick would resolve
	// one row and open another.
	unhealthyRuleAgain := &pb.GetSentryStatusResponse{
		State:  pb.SentryState_SENTRY_STATE_OK,
		Reason: "rule DENY_BURST unhealthy: panic: index 7 out of range",
		Rules:  []*pb.RuleStatus{{Rule: pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST, Healthy: false, LastError: "panic: index 7 out of range"}},
	}
	degradedAndUnhealthy := &pb.GetSentryStatusResponse{
		State:  pb.SentryState_SENTRY_STATE_DEGRADED,
		Reason: degrade + "; rule DENY_BURST unhealthy: panic: boom",
		Rules:  []*pb.RuleStatus{{Rule: pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST, Healthy: false, LastError: "panic: boom"}},
	}
	const cpuBad = "unavailable (incus client unavailable: boom)"
	cpuDown := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: cpuBad}
	cpuDownAgain := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: "unavailable (incus client unavailable: different text)"}
	cpuStandalone := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: "standalone (30s)"}
	cpuCollector := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: "metrics-collector"}
	degradedAndCPUDown := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_DEGRADED, Reason: degrade, CpuSampler: cpuBad}
