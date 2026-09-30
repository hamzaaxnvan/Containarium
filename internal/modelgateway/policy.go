package modelgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// The graduated response ladder: observe → throttle → alert → circuit-break →
// revoke.
//
// The reason for a ladder rather than a boolean is that both binary outcomes are
// wrong most of the time. "Allow" means a stolen token spends until a human
// notices, which is hours. "Deny" means one bad heuristic takes a customer's
// production agent offline, which is how enforcement gets turned off for good.
// Every state here except the last is reversible without a human, so the
// detector is allowed to be wrong: being wrong costs latency, not an outage.
//
// Only revocation is sticky, and only revocation is off by default.

// PolicyState is a tenant's rung on the ladder.
type PolicyState int

const (
	// StateObserve records usage and enforces nothing. The steady state.
	StateObserve PolicyState = iota

	// StateThrottle admits calls no faster than ThrottleMinInterval. A tenant
	// running away is slowed to a rate where a human can catch up with it,
	// without its work failing outright.
	StateThrottle

	// StateAlert is StateThrottle plus a one-shot alert to the sink. Split from
	// throttle so the paging threshold and the slowing threshold are tunable
	// independently: slowing is cheap, waking someone is not.
	StateAlert

	// StateCircuitBreak rejects every call for CircuitBreakFor, then steps back
	// down to StateAlert to re-evaluate. This is where an exceeded quota lands:
	// it clears itself as the rolling window slides, so a tenant that stops
	// spending recovers with no operator action.
	StateCircuitBreak

	// StateRevoke rejects every call until an operator clears it. The only
	// sticky state and the only one that needs a human to leave, so it is never
	// entered automatically unless RevokeAt is explicitly configured.
	StateRevoke
)

func (s PolicyState) String() string {
	switch s {
	case StateObserve:
		return "observe"
	case StateThrottle:
		return "throttle"
	case StateAlert:
		return "alert"
	case StateCircuitBreak:
		return "circuit_break"
	case StateRevoke:
		return "revoke"
	}
	return "unknown"
}

// MarshalJSON renders the state as its name, so the ops readout and any alert
// payload carry "circuit_break" rather than a bare 3.
func (s PolicyState) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// UnmarshalJSON accepts the name form this type emits, so /__gateway/policy and
// any persisted PolicyEvent round-trip. Without it, marshalling to a name makes
// the readout write-only for every Go consumer — the daemon and the CLI
// included. A bare number is also accepted so an older payload still decodes.
func (s *PolicyState) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] != '"' {
		var n int
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		if n < int(StateObserve) || n > int(StateRevoke) {
			return fmt.Errorf("modelgateway: unknown policy state %d", n)
		}
		*s = PolicyState(n)
		return nil
	}
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return err
	}
	for _, c := range []PolicyState{StateObserve, StateThrottle, StateAlert, StateCircuitBreak, StateRevoke} {
		if c.String() == name {
			*s = c
			return nil
		}
	}
	return fmt.Errorf("modelgateway: unknown policy state %q", name)
}

// PolicyEvent is emitted on every state transition. Carries attribution and the
// reason, never prompt or response content.
type PolicyEvent struct {
	Tenant   string      `json:"tenant"`
	Skill    string      `json:"skill,omitempty"`
	Provider string      `json:"provider,omitempty"`
	From     PolicyState `json:"from"`
	To       PolicyState `json:"to"`
	Score    float64     `json:"score"`
	Quota    float64     `json:"quota_fraction"`
	Reason   string      `json:"reason"`
	Signals  []Signal    `json:"signals,omitempty"`
	At       time.Time   `json:"at"`
}

// AlertSink receives policy transitions. Kept an interface, like UsageSink, so
// this package stays free of any alerting dependency; the daemon wires its own.
// A nil sink means transitions are logged and nothing else.
type AlertSink interface {
	PolicyTransition(ev PolicyEvent)
}

// PolicyConfig configures the ladder. The zero value is inert: no quota, no
// anomaly detection, every tenant in StateObserve. That is deliberate — an OSS
// operator who upgrades the daemon must not discover that their agents are
// suddenly being throttled by numbers they never chose.
type PolicyConfig struct {
	// Quota is the per-tenant budget. Zero disables quota enforcement.
	Quota QuotaLimits `json:"quota"`

	// Anomaly configures the detectors. Disabled by default.
	Anomaly AnomalyConfig `json:"anomaly"`

	// ThrottleAtQuota is the share of budget at which a tenant is slowed, ahead
	// of actually running out. Default 0.8. Exceeding the budget outright
	// (fraction >= 1) always circuit-breaks regardless of this.
	ThrottleAtQuota float64 `json:"throttle_at_quota"`

	// ThrottleMinInterval is the minimum spacing between admitted calls while
	// throttled. Default 2s.
	ThrottleMinInterval time.Duration `json:"throttle_min_interval"`

	// AlertAt / BreakAt / RevokeAt are anomaly-score thresholds. Defaults 0.3
	// and 0.6; RevokeAt defaults to 0, meaning automatic revocation is OFF and
	// a human must call Revoke. Set it (0..1] only where an operator is
	// prepared for a heuristic to cut a tenant off until someone intervenes.
	AlertAt  float64 `json:"alert_at"`
	BreakAt  float64 `json:"break_at"`
	RevokeAt float64 `json:"revoke_at"`

	// CircuitBreakFor is how long a break rejects before stepping down to
	// re-evaluate. Default 1m.
	CircuitBreakFor time.Duration `json:"circuit_break_for"`

	// Cooldown is how long a tenant must stay clean before de-escalating one
	// rung. Default 5m. Escalation is immediate; only the way down is damped,
	// so the ladder cannot flap a tenant between throttled and free while an
	// incident is still in progress.
	Cooldown time.Duration `json:"cooldown"`

	// Now is the clock, injectable for tests. Defaults to time.Now.
	Now func() time.Time `json:"-"`

	// Alerts receives transitions. Optional.
	Alerts AlertSink `json:"-"`
}

func (c PolicyConfig) normalized() PolicyConfig {
	c.Quota = c.Quota.normalized()
	c.Anomaly = c.Anomaly.normalized()
	if c.ThrottleAtQuota <= 0 || c.ThrottleAtQuota > 1 {
		c.ThrottleAtQuota = 0.8
	}
	if c.ThrottleMinInterval <= 0 {
		c.ThrottleMinInterval = 2 * time.Second
	}
	if c.AlertAt <= 0 {
		c.AlertAt = 0.3
	}
	if c.BreakAt <= 0 {
		c.BreakAt = 0.6
	}
	if c.CircuitBreakFor <= 0 {
		c.CircuitBreakFor = time.Minute
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 5 * time.Minute
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// Decision is the ladder's answer for one request.
type Decision struct {
	State      PolicyState   `json:"state"`
	Allow      bool          `json:"allow"`
	Status     int           `json:"status,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	Signals    []Signal      `json:"signals,omitempty"`
}

// tenantState is one tenant's accounting and rung. Guarded by its own mutex so
// tenants don't contend with each other on a busy gateway.
type tenantState struct {
	mu sync.Mutex

	// rateWindow backs both the quota check and the auth-failure ratio.
	rateWindow *rollingWindow
	// baseline is the tenant's learned normal token rate.
	baseline       *ewma
	lastBaselineAt time.Time

	models     *recentSet
	endpoints  *recentSet
	sourceNets *recentSet

	// pendingNew* carry this request's novelty findings into evaluate.
	pendingNewModel    string
	pendingNewEndpoint string

	state        PolicyState
	stateSince   time.Time
	lastTrigger  time.Time // last time a condition justified the current rung
	lastAdmitted time.Time
	breakUntil   time.Time
	manualReason string

	lastScore   float64
	lastQuota   float64
	lastSignals []Signal
}

// Policy is the per-tenant ladder. Safe for concurrent use.
type Policy struct {
	cfg PolicyConfig

	mu      sync.Mutex
	tenants map[string]*tenantState
}

// NewPolicy builds a Policy. A zero PolicyConfig yields an inert policy that
// allows everything and only records.
func NewPolicy(cfg PolicyConfig) *Policy {
	return &Policy{cfg: cfg.normalized(), tenants: map[string]*tenantState{}}
}

func (p *Policy) state(tenant string) *tenantState {
	p.mu.Lock()
	defer p.mu.Unlock()
	ts := p.tenants[tenant]
	if ts == nil {
		now := p.cfg.Now()
		ts = &tenantState{
			rateWindow:     newRollingWindow(p.cfg.Quota.Window),
			baseline:       newEWMA(p.cfg.Anomaly.BaselineAlpha),
			lastBaselineAt: now,
			models:         newRecentSet(p.cfg.Anomaly.NoveltyTTL),
			endpoints:      newRecentSet(p.cfg.Anomaly.NoveltyTTL),
			sourceNets:     newRecentSet(p.cfg.Anomaly.ConcurrentWindow),
			stateSince:     now,
		}
		p.tenants[tenant] = ts
	}
	return ts
}

// RequestInfo is what the ladder needs to know about one call. Everything here
// is metadata the gateway already has; no request body is retained.
type RequestInfo struct {
	Tenant     string
	Skill      string
	Provider   string
	Model      string
	Endpoint   string // upstream path shape, e.g. "/v1/messages"
	RemoteAddr string
}

// Check evaluates the ladder for one request and returns whether to admit it.
// Called after the gateway token is verified and before the real provider key is
// injected, so a denied request never reaches the provider and never touches the
// key.
func (p *Policy) Check(info RequestInfo) Decision {
	if info.Tenant == "" {
		return Decision{State: StateObserve, Allow: true}
	}
	now := p.cfg.Now()
	ts := p.state(info.Tenant)

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Novelty observations feed this evaluation, then reset.
	ts.pendingNewModel = ""
	ts.pendingNewEndpoint = ""
	if p.cfg.Anomaly.Enabled {
		if info.Model != "" && ts.models.observe(now, info.Model) {
			ts.pendingNewModel = info.Model
		}
		if info.Endpoint != "" && ts.endpoints.observe(now, info.Endpoint) {
			ts.pendingNewEndpoint = info.Endpoint
		}
		if n := sourceNet(info.RemoteAddr); n != "" {
			ts.sourceNets.observe(now, n)
		}
	}

	baselineDue := p.cfg.Anomaly.Enabled && now.Sub(ts.lastBaselineAt) >= p.cfg.Anomaly.BaselineWindow

	quota := p.cfg.Quota.evaluate(ts.rateWindow.sum(now))
	signals := p.cfg.Anomaly.evaluate(ts, now)
	sc := score(signals)

	// Fold the finished measurement period into the baseline — but only after
	// evaluating it, and only if it looked normal.
	//
	// Order matters more than it appears. Learning before evaluating lets the
	// spike being judged raise the very baseline it is judged against, and with
	// any reasonable EWMA weight that is enough to hide a 100x burst. Learning
	// from a window that fired signals, or one where the tenant was already off
	// StateObserve, has the same effect spread over a few windows: the detector
	// gradually accepts the attack as this tenant's new normal. So a window
	// teaches the baseline only if it was quiet.
	if baselineDue {
		if ts.state == StateObserve && len(signals) == 0 {
			ts.baseline.observe(ts.rateWindow.rate(now))
		}
		ts.lastBaselineAt = now
	}

	ts.lastScore, ts.lastQuota, ts.lastSignals = sc, quota.fraction, signals

	want, reason := p.target(quota, sc)
	p.transition(ts, info, want, reason, now, signals)

	return p.admit(ts, now, quota, signals)
}

// target maps the current measurements onto the rung they justify, with the
// reason that got them there.
func (p *Policy) target(q quotaUsage, sc float64) (PolicyState, string) {
	state, reason := StateObserve, ""

	if !p.cfg.Quota.IsZero() {
		switch {
		case q.exceeded != "":
			state, reason = StateCircuitBreak, "quota exceeded: "+p.cfg.Quota.describe(q)
		case q.fraction >= p.cfg.ThrottleAtQuota:
			state, reason = StateThrottle, "approaching quota: "+p.cfg.Quota.describe(q)
		}
	}

	if p.cfg.Anomaly.Enabled {
		var as PolicyState
		switch {
		case p.cfg.RevokeAt > 0 && sc >= p.cfg.RevokeAt:
			as = StateRevoke
		case sc >= p.cfg.BreakAt:
			as = StateCircuitBreak
		case sc >= p.cfg.AlertAt:
			as = StateAlert
		}
		if as > state {
			state, reason = as, fmt.Sprintf("anomaly score %.2f", sc)
		}
	}
	return state, reason
}

// transition moves the tenant onto want, applying immediate escalation and
// damped de-escalation.
func (p *Policy) transition(ts *tenantState, info RequestInfo, want PolicyState, reason string, now time.Time, sigs []Signal) {
	// Revocation outranks everything and never expires on its own — however it
	// was reached. Gating this on manualReason instead of the state itself
	// would leave an automatic revocation (RevokeAt) non-sticky: the
	// de-escalation case below would walk it back a rung per cooldown, so a
	// token revoked for a high anomaly score would regain access simply by
	// going quiet for five minutes. Only Clear leaves this state.
	if ts.state == StateRevoke {
		return
	}

	if want > StateObserve {
		ts.lastTrigger = now
	}

	switch {
	case want > ts.state:
		from := ts.state
		ts.state, ts.stateSince = want, now
		if want == StateCircuitBreak {
			ts.breakUntil = now.Add(p.cfg.CircuitBreakFor)
		}
		p.emit(ts, info, from, want, reason, now, sigs)

	case ts.state == StateCircuitBreak && now.After(ts.breakUntil):
		// A break is time-boxed: step down to alert and let the next
		// evaluation decide whether the condition is still live. Without this
		// a tenant that tripped once would stay broken until an operator
		// looked, which is the outage the ladder exists to avoid.
		//
		// Still-live conditions extend the break instead of stepping down, so a
		// tenant sitting over its budget stays broken quietly rather than
		// flapping break→alert→break and paging on every cycle.
		if want >= StateCircuitBreak {
			ts.breakUntil = now.Add(p.cfg.CircuitBreakFor)
			return
		}
		from := ts.state
		ts.state, ts.stateSince = StateAlert, now
		p.emit(ts, info, from, ts.state, "circuit-break elapsed; re-evaluating", now, sigs)

	case want < ts.state && now.Sub(ts.lastTrigger) >= p.cfg.Cooldown:
		from := ts.state
		ts.state, ts.stateSince = ts.state-1, now // one rung at a time
		p.emit(ts, info, from, ts.state, "cooldown elapsed", now, sigs)
	}
}

// admit applies the current rung to this request.
func (p *Policy) admit(ts *tenantState, now time.Time, q quotaUsage, sigs []Signal) Decision {
	d := Decision{State: ts.state, Allow: true, Signals: sigs}

	switch ts.state {
	case StateObserve:
		ts.lastAdmitted = now
		return d

	case StateThrottle, StateAlert:
		// A hard budget overrun is never merely slowed — target() will have
		// raised the rung, but a tenant sitting at exactly the limit while
		// de-escalating must still be stopped.
		if q.exceeded != "" {
			d.Allow, d.Status = false, http.StatusTooManyRequests
			d.Reason = "quota exceeded: " + p.cfg.Quota.describe(q)
			d.RetryAfter = p.cfg.Quota.Window
			ts.rateWindow.add(now, counts{policyDenials: 1})
			return d
		}
		if wait := p.cfg.ThrottleMinInterval - now.Sub(ts.lastAdmitted); wait > 0 && !ts.lastAdmitted.IsZero() {
			d.Allow, d.Status = false, http.StatusTooManyRequests
			d.Reason = "throttled"
			d.RetryAfter = wait
			ts.rateWindow.add(now, counts{policyDenials: 1})
			return d
		}
		ts.lastAdmitted = now
		return d

	case StateCircuitBreak:
		d.Allow, d.Status = false, http.StatusServiceUnavailable
		d.Reason = "circuit open"
		d.RetryAfter = ts.breakUntil.Sub(now)
		if d.RetryAfter < 0 {
			d.RetryAfter = 0
		}
		ts.rateWindow.add(now, counts{policyDenials: 1})
		return d

	case StateRevoke:
		d.Allow, d.Status = false, http.StatusForbidden
		d.Reason = "revoked"
		if ts.manualReason != "" {
			d.Reason = "revoked: " + ts.manualReason
		}
		ts.rateWindow.add(now, counts{policyDenials: 1})
		return d
	}
	return d
}

func (p *Policy) emit(ts *tenantState, info RequestInfo, from, to PolicyState, reason string, now time.Time, sigs []Signal) {
	if p.cfg.Alerts == nil || from == to {
		return
	}
	p.cfg.Alerts.PolicyTransition(PolicyEvent{
		Tenant:   info.Tenant,
		Skill:    info.Skill,
		Provider: info.Provider,
		From:     from,
		To:       to,
		Score:    ts.lastScore,
		Quota:    ts.lastQuota,
		Reason:   reason,
		Signals:  sigs,
		At:       now,
	})
}

// RecordUsage folds a completed call's token usage into the tenant's window, so
// the next Check sees what this one actually cost. Called after the response is
// metered — a request's own tokens are not known until it finishes, which is why
// a token budget is enforced one call late by construction.
func (p *Policy) RecordUsage(tenant string, u Usage) {
	if tenant == "" {
		return
	}
	now := p.cfg.Now()
	ts := p.state(tenant)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.rateWindow.add(now, counts{
		calls:        1,
		inputTokens:  u.InputTokens,
		outputTokens: u.OutputTokens,
		cachedTokens: u.CachedTokens,
	})
}

// RecordDenial records a request rejected before it reached the provider —
// an unknown provider for the token, a model outside its allowed set. These
// feed the auth-failure-ratio signal: a token being probed rather than used.
func (p *Policy) RecordDenial(tenant string) {
	if tenant == "" {
		return
	}
	now := p.cfg.Now()
	ts := p.state(tenant)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.rateWindow.add(now, counts{authDenials: 1})
}

// Revoke puts a tenant on the sticky rung until Clear. The operator escape
// hatch behind the issue's "instantly revocable" requirement: it takes effect on
// the tenant's next call with no token re-issue and no box restart.
func (p *Policy) Revoke(tenant, reason string) {
	if tenant == "" {
		return
	}
	now := p.cfg.Now()
	ts := p.state(tenant)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	from := ts.state
	ts.state, ts.stateSince, ts.manualReason = StateRevoke, now, reason
	p.emit(ts, RequestInfo{Tenant: tenant}, from, StateRevoke, "operator revoked: "+reason, now, nil)
}

// Clear returns a tenant to StateObserve, including from a manual revocation.
func (p *Policy) Clear(tenant string) {
	if tenant == "" {
		return
	}
	now := p.cfg.Now()
	ts := p.state(tenant)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	from := ts.state
	ts.state, ts.stateSince, ts.manualReason = StateObserve, now, ""
	ts.breakUntil = time.Time{}
	ts.lastTrigger = time.Time{}
	p.emit(ts, RequestInfo{Tenant: tenant}, from, StateObserve, "operator cleared", now, nil)
}

// TenantStatus is the ops readout for one tenant.
type TenantStatus struct {
	Tenant string      `json:"tenant"`
	State  PolicyState `json:"state"`
	Since  time.Time   `json:"since"`
	Score  float64     `json:"anomaly_score"`
	Quota  float64     `json:"quota_fraction"`
	Calls  int64       `json:"calls_in_window"`
	Tokens int64       `json:"tokens_in_window"`
	// AuthDenials are token-authorization failures; PolicyDenials are this
	// ladder's own refusals. Reported apart because conflating them is what
	// made enforcement self-amplifying — see counts in window.go.
	AuthDenials   int64    `json:"auth_denials_in_window"`
	PolicyDenials int64    `json:"policy_denials_in_window"`
	Baseline      float64  `json:"baseline_tokens_per_sec"`
	Samples       int      `json:"baseline_samples"`
	Models        []string `json:"models,omitempty"`
	SourceNets    []string `json:"source_nets,omitempty"`
	Signals       []Signal `json:"signals,omitempty"`
	RevokeCause   string   `json:"revoke_cause,omitempty"`
}

// Status snapshots every known tenant, worst rung first, for /__gateway/policy.
func (p *Policy) Status() []TenantStatus {
	now := p.cfg.Now()
	p.mu.Lock()
	names := make([]string, 0, len(p.tenants))
	states := make([]*tenantState, 0, len(p.tenants))
	for n, ts := range p.tenants {
		names = append(names, n)
		states = append(states, ts)
	}
	p.mu.Unlock()

	out := make([]TenantStatus, 0, len(names))
	for i, ts := range states {
		ts.mu.Lock()
		w := ts.rateWindow.sum(now)
		base, samples := ts.baseline.get()
		out = append(out, TenantStatus{
			Tenant:        names[i],
			State:         ts.state,
			Since:         ts.stateSince,
			Score:         ts.lastScore,
			Quota:         ts.lastQuota,
			Calls:         w.calls,
			Tokens:        w.totalTokens(),
			AuthDenials:   w.authDenials,
			PolicyDenials: w.policyDenials,
			Baseline:      base,
			Samples:       samples,
			Models:        ts.models.values(now),
			SourceNets:    ts.sourceNets.values(now),
			Signals:       ts.lastSignals,
			RevokeCause:   ts.manualReason,
		})
		ts.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State > out[j].State
		}
		return out[i].Tenant < out[j].Tenant
	})
	return out
}
