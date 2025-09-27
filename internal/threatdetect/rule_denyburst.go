package threatdetect

import (
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Default N/M for DenyBurstRule (design doc: "N default 20, M default 5
// minutes"). Operator-tunable via CONTAINARIUM_THREAT_DENY_BURST_N /
// CONTAINARIUM_THREAT_DENY_BURST_WINDOW_MINUTES (see internal/config) — no
// daemon rebuild required, just a restart with the new env var. Live tuning
// via an UpdateThreatRuleConfig RPC without a restart is #1643's scope.
const (
	DefaultDenyBurstN      = 20
	DefaultDenyBurstWindow = 5 * time.Minute
)

// denyBurstRecord is one deny event's evidence, kept in a tenant's sliding
// window until it ages out.
type denyBurstRecord struct {
	at     time.Time
	dst    string
	dport  uint32
	proto  string
	reason string
}

// DenyBurstRule fires a MEDIUM "fence probing" finding when one tenant
// accumulates >= N deny events within an M-minute sliding window — the
// signal that a tenant is testing the fence rather than having already
// breached it (CrossTenantFlowRule catches an actual breach). Per the
// engine's Rule.Sweep doc and the design doc's "deny-burst adds ≤ 30s sweep
// granularity", this rule accumulates on OnDeny but only evaluates and
// fires on Sweep — not on every OnDeny call — so a burst that never grows
// past a stale window doesn't keep re-alerting on the mere passage of time,
// and a quiet window doesn't emit anything between ticks.
type DenyBurstRule struct {
	n      int
	window time.Duration

	// countLogOnly makes would-deny events from an observation-only tenant
	// count toward the burst (#2612). Off by default.
	countLogOnly bool

	mu      sync.Mutex
	records map[string][]denyBurstRecord // tenantID -> window records, oldest first
	dirty   map[string]bool              // tenantID -> at least one OnDeny since the last Sweep
}

// NewDenyBurstRule builds the rule with N deny events within window
// triggering a finding. n<=0 or window<=0 falls back to the design doc's
// defaults rather than constructing a rule that can never fire (n=0) or
// fires on every single deny (window=0).
func NewDenyBurstRule(n int, window time.Duration) *DenyBurstRule {
	if n <= 0 {
		n = DefaultDenyBurstN
	}
	if window <= 0 {
		window = DefaultDenyBurstWindow
	}
