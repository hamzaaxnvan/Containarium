package tenantguard

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/nicguard"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// DefaultInterval is the steady-state reconcile cadence; bus events
// converge sooner.
const DefaultInterval = 60 * time.Second

// PolicySource is where the reconciler reads NetworkPolicy.allow_from_tenants
// (#2359). The daemon's network-policy store satisfies it; nil means no
// cross-tenant allow anywhere.
type PolicySource interface {
	List(ctx context.Context) ([]*pb.NetworkPolicy, error)
}

// PolicySourceFunc adapts a function to PolicySource (the daemon swaps its
// store after construction, so it passes a closure that reads the current
// one).
type PolicySourceFunc func(ctx context.Context) ([]*pb.NetworkPolicy, error)

func (f PolicySourceFunc) List(ctx context.Context) ([]*pb.NetworkPolicy, error) { return f(ctx) }

// Config is the reconciler's static wiring.
type Config struct {
	Mode nicguard.Mode
	// Policies supplies allow_from_tenants per tenant; optional.
	Policies PolicySource
	// Bridge is the Incus network tenant NICs sit on (e.g. incusbr0).
	Bridge string
	// BridgeCIDR is the bridge's ipv4.address as Incus reports it, gateway
	// form ("10.100.0.1/24"); network form is accepted.
	BridgeCIDR string
	// NICDevice is the device name to guard on each tenant container
	// (default eth0).
	NICDevice string
	// Interval overrides DefaultInterval (tests).
	Interval time.Duration
}

// Entry is one tenant container's guard state as of the last pass.
type Entry struct {
	Container string
	Tenant    string
	IP        string
	ACLName   string
	Attached  bool
	LastError string
}

// Status is the reconciler's last-pass snapshot.
type Status struct {
	Mode           nicguard.Mode
	FirewallDriver string
	Entries        []Entry
	// Unresolved lists Containarium-managed containers that are neither
	// core-role nor attributable to a tenant; they are left unguarded and
	// reported, and an unguarded managed box is a failed guard.
	Unresolved []string
	// Unmanaged lists instances carrying no user.containarium.* key at all
	// (made directly in Incus, or a VM). They are left unguarded and listed
	// for information; they are not Containarium's to attribute, so they do
	// not degrade the verdict (#2396).
	Unmanaged []string
	Tenants   int
	// Unsupported is set when this host cannot carry bridge NIC ACLs
	// (firewall driver or Incus too old): nothing is guarded, creates are
	// not refused, and LastError says why. Doctor treats it as red.
	Unsupported bool
	// LegacyIngressCleared counts per-container ACLs (acl-<user>) whose
	// pre-#2359 ingress rules this reconciler has cleared since start.
	LegacyIngressCleared int
	StaleSince           time.Time
	LastError            string
	LastPass             time.Time
}

// Reconciler keeps every tenant container's NIC ACL equal to what Compute
// says it should be. It only ever writes on drift.
type Reconciler struct {
	be  incus.Backend
	cfg Config

	mu     sync.Mutex
	status Status
	warned string // last unsupported reason logged

	// writeMu serialises every Incus write (ACL + NIC) across a reconcile
	// pass and Prepare. Without it the event-driven pass a create fires
	// and the ACL-at-birth hook for that same create race on one
	// instance, and Incus answers the loser with an ETag mismatch.
	writeMu sync.Mutex
}

// NewReconciler wires a reconciler; nothing runs until ReconcileOnce, Run
// or Prepare is called.
func NewReconciler(be incus.Backend, cfg Config) *Reconciler {
	if cfg.NICDevice == "" {
		cfg.NICDevice = "eth0"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Mode == "" {
		cfg.Mode = nicguard.ModeEnforce
	}
	return &Reconciler{be: be, cfg: cfg, status: Status{Mode: cfg.Mode}}
}

// Mode reports the arming state.
func (r *Reconciler) Mode() nicguard.Mode { return r.cfg.Mode }

// Status returns a copy of the last-pass snapshot.
func (r *Reconciler) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.status
	st.Entries = append([]Entry(nil), r.status.Entries...)
	st.Unresolved = append([]string(nil), r.status.Unresolved...)
	st.Unmanaged = append([]string(nil), r.status.Unmanaged...)
	return st
}

// Run reconciles at startup, on every bus event, and every Interval, until
// ctx is done. events is optional: a nil channel simply never fires.
func (r *Reconciler) Run(ctx context.Context, events <-chan struct{}) {
	if r.cfg.Mode != nicguard.ModeEnforce {
		log.Printf("[tenantguard] off (CONTAINARIUM_TENANT_GUARD=off): tenant containers can reach each other on %s", r.cfg.Bridge)
		return
	}
	if err := r.ReconcileOnce(ctx); err != nil {
		log.Printf("[tenantguard] reconcile: %v", err)
	}
	tick := time.NewTicker(r.cfg.Interval)
	defer tick.Stop()
	log.Printf("[tenantguard] started (bridge=%s, interval=%s, ENFORCE)", r.cfg.Bridge, r.cfg.Interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-events:
		}
		if err := r.ReconcileOnce(ctx); err != nil {
			log.Printf("[tenantguard] reconcile: %v", err)
		}
	}
}

// Prepare guards a container that was just created and is not yet started
// (docs/architecture/tenant-network-guard.md, "ACL-at-birth"): the tenant's
// ACL is created if missing (with whatever siblings the host currently
// has; this box's own address is added by the next pass) and attached to
// the instance-local NIC. A no-op when the guard is off. Any failure is
// returned so the caller can refuse the create rather than start a box
// that co-tenants can reach.
func (r *Reconciler) Prepare(ctx context.Context, containerName, tenant string) error {
	return r.prepare(ctx, containerName, tenant, false)
}

// PrepareNetworkTenant requires working ENFORCE + NIC-ACL support at birth.
// In contrast to legacy Prepare it never silently skips unsupported hosts.
func (r *Reconciler) PrepareNetworkTenant(ctx context.Context, containerName, tenant string) error {
	return r.prepare(ctx, containerName, tenant, true)
}

func (r *Reconciler) prepare(ctx context.Context, containerName, tenant string, required bool) error {
	if r.cfg.Mode != nicguard.ModeEnforce {
		if required {
			return fmt.Errorf("explicit network Tenant requires ENFORCE NIC guard")
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(tenant) == "" {
		return fmt.Errorf("tenantguard: %s has no resolvable tenant", containerName)
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if _, err := nicguard.CheckSupport(r.be); err != nil {
		if nicguard.Unsupported(err) {
			if required {
				return fmt.Errorf("explicit network Tenant requires supported NIC ACLs: %w", err)
			}
			// This host cannot carry bridge NIC ACLs at all (driver or
			// Incus too old). Refusing every create here would turn a
			// missing capability into an outage; the pass reports it and
			// doctor goes red instead. A capable host that fails to guard
			// one box still fails closed below.
			r.warnUnsupported(err)
			return nil
		}
		return fmt.Errorf("tenantguard: %w", err)
	}
	in, _, _, err := r.gather(ctx)
	if err != nil {
		return fmt.Errorf("tenantguard: %w", err)
	}
	// The box was just created and may not be listed yet, or be listed
