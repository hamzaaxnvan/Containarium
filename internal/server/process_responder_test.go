package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeActuator is the containerActuator test double: an in-memory
// limits.cpu / limits.cpu.allowance pair and a frozen flag per container,
// plus a scripted failure.
type fakeActuator struct {
	mu     sync.Mutex
	count  map[string]string
	allow  map[string]string
	frozen map[string]bool
	// ids is each box's Incus instance id (volatile.uuid); a box with none
	// set reads as "id-<name>". A box listed in missing answers not found.
	ids     map[string]string
	missing map[string]bool
	fail    error
	calls   []string
}

func (a *fakeActuator) InstanceID(name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.missing[name] {
		return "", api.StatusErrorf(http.StatusNotFound, "Instance not found")
	}
	if id, ok := a.ids[name]; ok {
		return id, nil
	}
	return "id-" + name, nil
}

func newFakeActuator() *fakeActuator {
	return &fakeActuator{count: map[string]string{}, allow: map[string]string{}, frozen: map[string]bool{},
		ids: map[string]string{}, missing: map[string]bool{}}
}

func (a *fakeActuator) CPULimit(name string) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "CPULimit:"+name)
	if a.fail != nil {
		return "", "", a.fail
	}
	return a.count[name], a.allow[name], nil
}

func (a *fakeActuator) SetCPULimit(name, count, allowance string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "SetCPULimit:"+name+":"+count+":"+allowance)
	if a.fail != nil {
		return a.fail
	}
	a.count[name], a.allow[name] = count, allowance
	return nil
}

func (a *fakeActuator) Freeze(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "Freeze:"+name)
	if a.fail != nil {
		return a.fail
	}
	a.frozen[name] = true
	return nil
}

func (a *fakeActuator) Unfreeze(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, "Unfreeze:"+name)
	if a.fail != nil {
		return a.fail
	}
	a.frozen[name] = false
	return nil
}

// fakeFindings is an in-memory findingLifecycle: enough of a finding store
// for the responder (Get/Resolve/SetResponse/Upsert) without Postgres or
// the audit chain that MemFindingStore needs.
type fakeFindings struct {
	// beforeGet is a test-only state-change hook; set before starting a job.
	beforeGet func()
	mu        sync.Mutex
	nextID    int64
	rows      map[int64]*threatdetect.Finding
	// writes is every response handed to SetResponse, as written (before
	// the sticky process_killed merge), so a test can tell what a path
	// recorded from what the store kept.
	writes []threatdetect.FindingResponse
	// held is every box record SetResponseHeld delivered without storing
	// (#2820); resolvedWith every response ResolveWithResponse resolved with.
	held, resolvedWith []threatdetect.FindingResponse
	// notifier, when set, gets what each write would deliver, as the real
	// stores hand it (threatdetect.ResponseDelivery), so a test can run the
	// real webhook gate behind the responder.
	notifier threatdetect.Notifier
}

// notify hands f to the notifier. Called with s.mu held, unlike the real
// stores; the WebhookNotifier never calls back into the store.
func (s *fakeFindings) notify(f *threatdetect.Finding) {
	if s.notifier != nil {
		s.notifier.Notify(f)
	}
}

func newFakeFindings() *fakeFindings { return &fakeFindings{rows: map[int64]*threatdetect.Finding{}} }

func (s *fakeFindings) Upsert(_ context.Context, f *threatdetect.Finding) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.State == threatdetect.FindingStateOpen && r.Rule == f.Rule && r.TenantID == f.TenantID && r.Subject == f.Subject {
			r.Count++
			cp := *r // a real store returns a fresh row, never its own
			if cp.Response != nil {
				resp := *cp.Response
				cp.Response = &resp
			}
			delivered := cp
			delivered.Container = f.Container // the Postgres store returns the writer's box
			defer s.notify(&delivered)
			return &cp, nil
		}
	}
	s.nextID++
	f.ID = s.nextID
	f.State = threatdetect.FindingStateOpen
	f.Count = 1
	s.rows[f.ID] = f
	cp := *f
	defer s.notify(&cp)
	return &cp, nil
}

func (s *fakeFindings) Get(_ context.Context, id int64) (*threatdetect.Finding, error) {
	if s.beforeGet != nil {
		s.beforeGet()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.rows[id]; ok {
		cp := *f
		return &cp, nil
	}
	return nil, threatdetect.ErrFindingNotFound
}

func (s *fakeFindings) Resolve(_ context.Context, id int64) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.rows[id]
	if !ok {
		return nil, threatdetect.ErrFindingNotFound
	}
	if f.State != threatdetect.FindingStateOpen {
		return nil, threatdetect.ErrFindingNotOpen
	}
	f.State = threatdetect.FindingStateResolved
	cp := *f
	defer s.notify(&cp)
	return f, nil
}

func (s *fakeFindings) SetResponse(_ context.Context, id int64, r threatdetect.FindingResponse) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setResponseLocked(id, r, false)
}

// setResponseLocked is SetResponse with s.mu held; resolve also resolves an
// open finding in the same write, delivered once as the resolve.
func (s *fakeFindings) setResponseLocked(id int64, r threatdetect.FindingResponse, resolve bool) (*threatdetect.Finding, error) {
	f, ok := s.rows[id]
	if !ok {
		return nil, threatdetect.ErrFindingNotFound
	}
	s.writes = append(s.writes, r)
	resp := r
	// process_killed is sticky, as in both real stores (#2766).
	resp.KeepKillOf(f.Response)
	f.Response = &resp
	notOpen := resolve && f.State != threatdetect.FindingStateOpen
	if resolve && !notOpen {
		f.State = threatdetect.FindingStateResolved
		s.resolvedWith = append(s.resolvedWith, r)
	}
	cp := *f
	s.notify(threatdetect.ResponseDelivery(&cp, r))
	if notOpen {
		return &cp, threatdetect.ErrFindingNotOpen
	}
	return &cp, nil
}

// SetResponseHeld stores standing and records box as the write (#2820).
func (s *fakeFindings) SetResponseHeld(_ context.Context, id int64, box, standing threatdetect.FindingResponse) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.rows[id]
	if !ok {
		return nil, threatdetect.ErrFindingNotFound
	}
	s.writes = append(s.writes, box)
	s.held = append(s.held, box)
	resp := standing
	resp.KeepKillOf(f.Response)
	f.Response = &resp
	cp := *f
	s.notify(threatdetect.ResponseDelivery(&cp, box))
	return &cp, nil
}

// ResolveWithResponse stores r and resolves, as one write (#2820); a finding
// no longer open still gets r and ErrFindingNotOpen, as the real stores do.
func (s *fakeFindings) ResolveWithResponse(_ context.Context, id int64, r threatdetect.FindingResponse) (*threatdetect.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setResponseLocked(id, r, true)
}

// lastWrite is the response most recently handed to SetResponse.
func (s *fakeFindings) lastWrite() threatdetect.FindingResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes[len(s.writes)-1]
}

func (s *fakeFindings) byRule(rule pb.ThreatRuleId) []*threatdetect.Finding {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*threatdetect.Finding
	for _, f := range s.rows {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []*audit.AuditEntry
}

func (a *fakeAudit) Log(_ context.Context, e *audit.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *fakeAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.entries {
		if e.ResourceType == "security.action" {
			out = append(out, e.Action)
		}
	}
	return out
}

type responderFixture struct {
	r        *ProcessResponder
	act      *fakeActuator
	findings *fakeFindings
	actions  threatdetect.ActionStore
	policies *threatdetect.ProcessPolicyStore
	audit    *fakeAudit
	clock    time.Time
}

func newResponderFixture(t *testing.T, degraded bool) *responderFixture {
	t.Helper()
	fx := &responderFixture{
		act:      newFakeActuator(),
		findings: newFakeFindings(),
		actions:  threatdetect.NewMemActionStore(),
		policies: threatdetect.NewProcessPolicyStore(nil),
		audit:    &fakeAudit{},
		clock:    time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	fx.r = NewProcessResponder(fx.policies, fx.actions, fx.findings, fx.act, fx.audit, degraded)
	fx.r.now = func() time.Time { return fx.clock }
	return fx
}

func (fx *responderFixture) setDefault(t *testing.T, action pb.ProcessAction, allowance string, ttl time.Duration) {
	t.Helper()
	p := &pb.ProcessPolicy{Action: action, ThrottleAllowance: allowance}
	if ttl != 0 {
		p.Ttl = durationpb.New(ttl)
	}
	if err := fx.policies.SetDefault(context.Background(), p); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
}

// fire upserts a HIGH mining-behavior finding through the ActingSink, the
// way the engine does in production, and returns the stored finding.
func (fx *responderFixture) fire(t *testing.T, container string, rule pb.ThreatRuleId, sev pb.ThreatSeverity) *threatdetect.Finding {
	t.Helper()
	sink := NewActingSink(fx.findings, fx.r)
	out, err := sink.Upsert(context.Background(), &threatdetect.Finding{
		Rule: rule, Severity: sev, TenantID: "tenant-a", Container: container, Subject: "203.0.113.9:3333",
	})
	if err != nil {
		t.Fatalf("Upsert through ActingSink: %v", err)
	}
	return out
}

const (
	ruleMiningBehavior = pb.ThreatRuleId(6) // THREAT_RULE_ID_MINING_BEHAVIOR (#2635)
	ruleMinerSignature = pb.ThreatRuleId_THREAT_RULE_ID_MINER_SIGNATURE
)

// The responder's action table: what each policy does to the box, what it
// records, and what it leaves alone.
func TestProcessResponder_Apply(t *testing.T) {
	tests := []struct {
		name         string
		policy       pb.ProcessAction
		rule         pb.ThreatRuleId
		severity     pb.ThreatSeverity
		degraded     bool
		wantAction   pb.ProcessAction
		wantState    pb.ResponseState
		wantAllow    string
		wantFrozen   bool
		wantOpenRow  bool
		wantAuditOps []string
	}{
		{
			name: "ALERT does nothing to the box", policy: pb.ProcessAction_PROCESS_ACTION_ALERT,
			rule: ruleMiningBehavior, severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH,
			wantAction: pb.ProcessAction_PROCESS_ACTION_ALERT, wantState: pb.ResponseState_RESPONSE_STATE_NONE,
			wantAllow: "200ms/100ms", wantAuditOps: []string{"alert"},
		},
		{
			name: "THROTTLE records the previous limit and applies the allowance", policy: pb.ProcessAction_PROCESS_ACTION_THROTTLE,
			rule: ruleMiningBehavior, severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH,
			wantAction: pb.ProcessAction_PROCESS_ACTION_THROTTLE, wantState: pb.ResponseState_RESPONSE_STATE_APPLIED,
			wantAllow: "10ms/100ms", wantOpenRow: true, wantAuditOps: []string{"apply"}, // 10% of ONE core as a hard CFS quota, never Incus's soft "N%" share (#2676)
		},
		{
			name: "FREEZE freezes and keeps the limit", policy: pb.ProcessAction_PROCESS_ACTION_FREEZE,
			rule: ruleMinerSignature, severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH,
			wantAction: pb.ProcessAction_PROCESS_ACTION_FREEZE, wantState: pb.ResponseState_RESPONSE_STATE_APPLIED,
			wantAllow: "200ms/100ms", wantFrozen: true, wantOpenRow: true, wantAuditOps: []string{"apply"},
		},
		{
			name: "MEDIUM finding never acts", policy: pb.ProcessAction_PROCESS_ACTION_THROTTLE,
			rule: ruleMiningBehavior, severity: pb.ThreatSeverity_THREAT_SEVERITY_MEDIUM,
			wantAllow: "200ms/100ms",
		},
		{
			name: "a rule outside the mining pair never acts", policy: pb.ProcessAction_PROCESS_ACTION_FREEZE,
			rule: pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION, severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH,
			wantAllow: "200ms/100ms",
		},
		{
			name: "degraded store forces ALERT over a THROTTLE policy", policy: pb.ProcessAction_PROCESS_ACTION_THROTTLE,
			rule: ruleMiningBehavior, severity: pb.ThreatSeverity_THREAT_SEVERITY_HIGH, degraded: true,
			wantAction: pb.ProcessAction_PROCESS_ACTION_ALERT, wantState: pb.ResponseState_RESPONSE_STATE_NONE,
			wantAllow: "200ms/100ms", wantAuditOps: []string{"alert"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newResponderFixture(t, tt.degraded)
