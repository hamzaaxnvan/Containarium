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
