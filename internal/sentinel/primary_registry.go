package sentinel

import (
	"strings"
	"sync"
	"time"
)

// PrimaryTTL is the maximum age of a Primary registration before it's considered
// stale and may be evicted. Primaries are expected to heartbeat every PrimaryTTL/3.
const PrimaryTTL = 90 * time.Second

// Primary represents a registered primary daemon serving one pool.
type Primary struct {
	Pool     Pool     `json:"pool"`
	Hostname string   `json:"hostname"`          // primary's own subdomain (e.g. prod.example.com)
	Aliases  []string `json:"aliases,omitempty"` // additional hostnames the primary's Caddy routes (e.g. api.example.com, voice.example.com)
	// BaseDomains anchor suffix routing: any inbound SNI of the form
	// "<anything>.<one-of-BaseDomains>" routes to this primary, so
	// containers that get an ad-hoc subdomain via expose_port don't
	// need to be re-registered as aliases. A primary can advertise
	// multiple base domains so one backend can host workloads that
	// publish under different parent domains (e.g. a lab backend
	// serving both *.lab.example.com and *.demo.example.org).
	// Empty (the default) disables suffix matching for this primary
	// — exact Hostname/Aliases still work. See docs/PER-POOL-BASE-DOMAIN.md.
	BaseDomains   []string  `json:"base_domains,omitempty"`
	IP            string    `json:"ip"`   // primary's reachable IP (typically internal VPC IP)
	Port          int       `json:"port"` // HTTPS port on the primary (typically 443)
	BackendID     string    `json:"backend_id,omitempty"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	// AliasHealth is the result of the most recent end-to-end reachability
	// probe (see checkTunnelPrimaries) of Hostname and each entry in
	// Aliases. Only populated for tunnel-backed primaries (BackendID set) —
	// registration alone proves the sentinel can route SNI here, not that
	// the primary's own Caddy actually serves each declared hostname
	// (#1872: a primary can be fully registered here and still 502 for an
	// alias its Caddy was never configured with).
	AliasHealth []AliasHealth `json:"alias_health,omitempty"`
	// rev is bumped on every Register() call for this pool (see
	// PrimaryRegistry.nextRev) and threaded through to SetAliasHealth by
	// checkTunnelPrimaries. It exists so a reachability sweep that started
	// against an old declaration (before a Register() replaced
	// Hostname/Aliases/BackendID mid-sweep) can't have its stale results
	// attributed to the new declaration — SetAliasHealth discards a rev
	// that no longer matches. Not exposed via JSON; internal bookkeeping.
	rev uint64
}

// AliasHealth records one hostname's most recent reachability probe
// result for a tunnel-promoted primary. See Manager.checkTunnelPrimaries.
type AliasHealth struct {
	Hostname  string    `json:"hostname"`
	Reachable bool      `json:"reachable"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// PrimaryRegistry tracks pool → primary mappings populated by daemon
// self-registration. Entries that don't heartbeat within PrimaryTTL are
// evicted on read.
type PrimaryRegistry struct {
	mu        sync.RWMutex
	primaries map[Pool]*Primary
	now       func() time.Time
	// nextRev hands out Primary.rev values. Monotonic across all pools;
	// only comparisons within one pool's successive registrations are
	// meaningful. Same idiom as TunnelSpot.Generation.
	nextRev uint64
}

// NewPrimaryRegistry creates an empty registry.
func NewPrimaryRegistry() *PrimaryRegistry {
	return &PrimaryRegistry{
		primaries: make(map[Pool]*Primary),
		now:       time.Now,
	}
}

// Register inserts or updates a primary. Pool must be non-empty (one primary
// per pool). The RegisteredAt timestamp is preserved on update.
func (r *PrimaryRegistry) Register(p Primary) *Primary {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.nextRev++
	rev := r.nextRev
	if existing, ok := r.primaries[p.Pool]; ok {
		// Update fields that can change, keep original RegisteredAt
		existing.Hostname = p.Hostname
		existing.Aliases = p.Aliases
		existing.BaseDomains = p.BaseDomains
		existing.IP = p.IP
		existing.Port = p.Port
		existing.BackendID = p.BackendID
		existing.LastHeartbeat = now
		// The declaration just changed identity (new hostname/aliases/
		// backend) — any AliasHealth on file was probed against the OLD
		// declaration and would otherwise linger under the new one,
		// misattributed. Clearing it here plus bumping rev means an
		// in-flight sweep from before this Register() call gets its
		// SetAliasHealth rejected below rather than clobbering (or being
		// clobbered by) the fresh declaration.
		existing.AliasHealth = nil
		existing.rev = rev
		return existing
	}

	stored := p
	stored.RegisteredAt = now
	stored.LastHeartbeat = now
	stored.rev = rev
	r.primaries[p.Pool] = &stored
	return &stored
}

// SetAliasHealth records the latest reachability probe results for pool's
// Hostname/Aliases (see Manager.checkTunnelPrimaries), scoped to the
// declaration revision (rev) the probe actually ran against — the Primary
// snapshot's rev field, from PrimaryRegistry.All() at the time the sweep
// started. No-op if pool isn't currently registered (a probe result racing
// an Unregister/disconnect can't resurrect a stale entry) or if rev no
// longer matches the current declaration (Register() replaced it —
// possibly with different Hostname/Aliases — while the sweep was still
// running against the old one).
func (r *PrimaryRegistry) SetAliasHealth(pool Pool, rev uint64, health []AliasHealth) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.primaries[pool]
	if !ok || p.rev != rev {
		return
	}
	p.AliasHealth = health
}

// Heartbeat refreshes the LastHeartbeat timestamp for a pool. Returns the
// updated primary, or nil if the pool isn't registered.
func (r *PrimaryRegistry) Heartbeat(pool Pool) *Primary {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.primaries[pool]
	if !ok {
		return nil
	}
	p.LastHeartbeat = r.now()
	return p
}

// Unregister removes a primary by pool name. Returns true if it existed.
func (r *PrimaryRegistry) Unregister(pool Pool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.primaries[pool]
	delete(r.primaries, pool)
	return ok
}

// UnregisterByBackendID removes any primary entry whose BackendID matches.
// Used when a tunnel-registered primary disconnects. Returns the number of
// entries removed.
func (r *PrimaryRegistry) UnregisterByBackendID(backendID string) int {
