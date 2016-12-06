package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantRegistry assigns a stable, non-zero uint32 ID to each tenant name. The
// eBPF network-policy maps (#315) key everything on this ID — a container's
// veth_policy entry, its egress_cidr LPM entries, and the ip_tenant map all
// carry the tenant's uint32. The ID MUST be stable across daemon restarts: the
// BPF maps are repopulated from stored policies + live containers on boot, and
// if a tenant's ID changed between runs its egress rules would be keyed to a
// different ID than its containers — silently misrouting policy. So this is a
// registry (assign once, persist, reuse), NOT a hash of the tenant name (a hash
// collision would merge two tenants' policies — unacceptable for a security
// control).
//
// ID 0 is reserved: the BPF programs treat a zero tenant_id / absent map entry
// as "unmanaged", so assigned IDs start at 1.
type TenantRegistry interface {
	// ID returns the tenant's stable ID, assigning (and persisting) a new one on
	// first use. Idempotent: repeated calls for the same tenant return the same ID.
	ID(ctx context.Context, tenant string) (uint32, error)
}

// --- in-memory ------------------------------------------------------

// MemTenantRegistry assigns IDs from a monotonic counter held in memory. IDs do
// not survive a restart — used on --standalone daemons (no Postgres) and tests.
// On a standalone daemon that's acceptable: the maps are rebuilt from scratch on
// boot anyway, and a single run is internally consistent.
type MemTenantRegistry struct {
	mu   sync.Mutex
	ids  map[string]uint32
	next uint32
}

func NewMemTenantRegistry() *MemTenantRegistry {
	return &MemTenantRegistry{ids: make(map[string]uint32), next: 1}
}

func (r *MemTenantRegistry) ID(_ context.Context, tenant string) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.ids[tenant]; ok {
		return id, nil
	}
	id := r.next
	r.ids[tenant] = id
