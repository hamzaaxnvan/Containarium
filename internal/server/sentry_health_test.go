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
	const execBad = "unavailable (netbpf: execwatch: load collection: BTF not found)"
	execDown := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: "metrics-collector", ExecTelemetry: execBad}
	execDownAgain := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: "metrics-collector", ExecTelemetry: "unavailable (netbpf: execwatch: attach tracepoint sched/sched_process_exec: different text)"}
	execOK := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: "metrics-collector", ExecTelemetry: "ok", ExecEventsLost: 12}
	execAndCPUDown := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_OK, CpuSampler: cpuBad, ExecTelemetry: execBad}
	degradedAndExecDown := &pb.GetSentryStatusResponse{State: pb.SentryState_SENTRY_STATE_DEGRADED, Reason: degrade, CpuSampler: "metrics-collector", ExecTelemetry: execBad}
	tests := []struct {
		name         string
		steps        []*pb.GetSentryStatusResponse
		wantOpen     []string // subjects of open rows, in insertion order
		wantResolved int
		wantCount    int64  // count on the single open row, when wantOpen has one
		wantNote     string // evidence note on the open row(s); "" = same as the subject
	}{
		{name: "unavailable raises one medium finding", steps: []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, noObj)}, wantOpen: []string{noObj}, wantCount: 1},
		{name: "degraded raises", steps: []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_DEGRADED, degrade)}, wantOpen: []string{degrade}, wantCount: 1},
		// Findings persist but the responder is ALERT-only (#2719). This only
		// pins the status → finding mapping; the real proof (the server's own
		// status driving the loop) is TestSentryHealth_RaisesFindingWhenOnlyResponderDegraded.
		{name: "responder-only degraded raises", steps: []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_DEGRADED, responderDown)}, wantOpen: []string{responderDown}, wantCount: 1},
		{name: "unhealthy rule raises with a stable subject and the full reason as the note", steps: []*pb.GetSentryStatusResponse{unhealthyRule}, wantOpen: []string{"rule DENY_BURST unhealthy"}, wantCount: 1, wantNote: unhealthyRule.Reason},
		{
			name:      "unhealthy rule with changing panic text dedupes into one row",
			steps:     []*pb.GetSentryStatusResponse{unhealthyRule, unhealthyRuleAgain},
			wantOpen:  []string{"rule DENY_BURST unhealthy"},
			wantCount: 2,
			wantNote:  unhealthyRule.Reason, // the fake keeps the first sighting's evidence
		},
		{name: "degraded plus unhealthy rule drops only the panic text", steps: []*pb.GetSentryStatusResponse{degradedAndUnhealthy}, wantOpen: []string{degrade + "; rule DENY_BURST unhealthy"}, wantCount: 1, wantNote: degradedAndUnhealthy.Reason},
		{
			name:      "cpu sampler unavailable raises with a stable subject and the full status as the note",
			steps:     []*pb.GetSentryStatusResponse{cpuDown},
			wantOpen:  []string{"cpu-sampler unavailable"},
			wantCount: 1,
			wantNote:  "cpu-sampler: " + cpuBad,
		},
		{
			name:      "cpu sampler unavailable with changing detail dedupes into one row",
			steps:     []*pb.GetSentryStatusResponse{cpuDown, cpuDownAgain},
			wantOpen:  []string{"cpu-sampler unavailable"},
			wantCount: 2,
			wantNote:  "cpu-sampler: " + cpuBad, // the fake keeps the first sighting's evidence
		},
		{name: "cpu sampler recovering to standalone resolves", steps: []*pb.GetSentryStatusResponse{cpuDown, cpuStandalone}, wantResolved: 1},
		{name: "cpu sampler on the metrics collector from the start raises nothing", steps: []*pb.GetSentryStatusResponse{cpuCollector}},
		{
			name:      "degraded plus cpu sampler unavailable reports both in one finding",
			steps:     []*pb.GetSentryStatusResponse{degradedAndCPUDown},
			wantOpen:  []string{degrade + "; cpu-sampler unavailable"},
			wantCount: 1,
			wantNote:  degrade + "; cpu-sampler: " + cpuBad,
		},
		{
			name:      "exec telemetry unavailable raises with a stable subject and the full status as the note",
			steps:     []*pb.GetSentryStatusResponse{execDown},
			wantOpen:  []string{"exec-telemetry unavailable"},
			wantCount: 1,
			wantNote:  "exec-telemetry: " + execBad,
		},
		{
			name:      "exec telemetry unavailable with changing detail dedupes into one row",
			steps:     []*pb.GetSentryStatusResponse{execDown, execDownAgain},
			wantOpen:  []string{"exec-telemetry unavailable"},
			wantCount: 2,
			wantNote:  "exec-telemetry: " + execBad,
		},
		{name: "exec telemetry recovering to ok resolves", steps: []*pb.GetSentryStatusResponse{execDown, execOK}, wantResolved: 1},
		{name: "exec telemetry ok with a lost count raises nothing (lost is a counter, not a fault)", steps: []*pb.GetSentryStatusResponse{execOK}},
		{
			name:      "exec telemetry and cpu sampler both down are one finding naming both",
			steps:     []*pb.GetSentryStatusResponse{execAndCPUDown},
			wantOpen:  []string{"cpu-sampler unavailable; exec-telemetry unavailable"},
			wantCount: 1,
			wantNote:  "cpu-sampler: " + cpuBad + "; exec-telemetry: " + execBad,
		},
		{
			name:      "degraded plus exec telemetry unavailable reports both in one finding",
			steps:     []*pb.GetSentryStatusResponse{degradedAndExecDown},
			wantOpen:  []string{degrade + "; exec-telemetry unavailable"},
			wantCount: 1,
			wantNote:  degrade + "; exec-telemetry: " + execBad,
		},
		{name: "ok raises nothing", steps: []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_OK, "")}},
		{name: "disabled raises nothing", steps: []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_DISABLED, "CONTAINARIUM_THREAT_SENTRY is not set")}},
		{
			name:      "same reason repeated dedupes into one row with a count",
			steps:     []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, noObj), statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, noObj), statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, noObj)},
			wantOpen:  []string{noObj},
			wantCount: 3,
		},
		{
			name:         "reason change opens a new subject and resolves the old",
			steps:        []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, noObj), statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, failed)},
			wantOpen:     []string{failed},
			wantResolved: 1,
			wantCount:    1,
		},
		{
			name:         "back to ok resolves the open finding",
			steps:        []*pb.GetSentryStatusResponse{statusResp(pb.SentryState_SENTRY_STATE_UNAVAILABLE, noObj), statusResp(pb.SentryState_SENTRY_STATE_OK, "")},
			wantResolved: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeHealthStore{}
			i := 0
			h := NewSentryHealth(func() *pb.GetSentryStatusResponse { return tt.steps[i] }, store, func() string { return "backend-1" }, nil, time.Minute)
			for i = range tt.steps {
				h.Check(context.Background())
			}
			open := store.open()
			if len(open) != len(tt.wantOpen) {
				t.Fatalf("open rows = %d (%+v), want %d", len(open), open, len(tt.wantOpen))
			}
			for j, f := range open {
				if f.Subject != tt.wantOpen[j] {
					t.Errorf("open[%d].Subject = %q, want %q", j, f.Subject, tt.wantOpen[j])
				}
				if f.Rule != pb.ThreatRuleId_THREAT_RULE_ID_SENTRY_UNAVAILABLE || f.Severity != pb.ThreatSeverity_THREAT_SEVERITY_MEDIUM {
					t.Errorf("open[%d] rule/severity = %v/%v, want SENTRY_UNAVAILABLE/MEDIUM", j, f.Rule, f.Severity)
				}
				if f.TenantID != "" || f.Container != "" || f.BackendID != "backend-1" {
					t.Errorf("open[%d] tenant/container/backend = %q/%q/%q, want \"\"/\"\"/backend-1", j, f.TenantID, f.Container, f.BackendID)
				}
				wantNote := tt.wantNote
				if wantNote == "" {
					wantNote = f.Subject
				}
				if len(f.Evidence.Configs) != 1 || f.Evidence.Configs[0].Note != wantNote {
					t.Errorf("open[%d] evidence = %+v, want one ConfigEvidence{Note: %q}", j, f.Evidence, wantNote)
				}
				if len(open) == 1 && f.Count != tt.wantCount {
					t.Errorf("count = %d, want %d", f.Count, tt.wantCount)
				}
			}
			resolved := 0
			for _, r := range store.rows {
				if r.State == threatdetect.FindingStateResolved {
					resolved++
				}
			}
			if resolved != tt.wantResolved {
				t.Errorf("resolved rows = %d, want %d", resolved, tt.wantResolved)
			}
		})
	}
}

// A restarted daemon must resolve a SENTRY_UNAVAILABLE row an earlier
// process left open once the sentry is healthy — otherwise a persisted
// finding outlives the fault forever. Rows for other backends or rules are
// left alone.
func TestSentryHealthCheck_ResolvesRowLeftOpenByPreviousProcess(t *testing.T) {
	store := &fakeHealthStore{}
