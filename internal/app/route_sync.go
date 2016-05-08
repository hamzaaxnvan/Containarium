package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// WakeTracker is the subset of *wake.WakeStateTracker that RouteSyncJob
// reads when deciding what upstream to push to Caddy. Declared as an
// interface so the wake/app import direction stays one-way (wake →
// app) — app/route_sync.go would import internal/wake otherwise and
// create a cycle. Satisfied by *wake.WakeStateTracker.
type WakeTracker interface {
	IsInWakeMode(containerName string) (host string, port int, ok bool)
}

// RouteSyncJob synchronizes routes from PostgreSQL (source of truth) to Caddy (runtime cache)
type RouteSyncJob struct {
	routeStore     *RouteStore
	proxyManager   *ProxyManager
	l4ProxyManager *L4ProxyManager // optional, for tls_passthrough routes
	wakeTracker    WakeTracker     // optional; when set, sleeping containers route to daemon's wake handler
	interval       time.Duration

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// l4Activated latches once the layer4 app has been brought up, and is
	// what lets the sync tell "never needed L4" apart from "had L4 and lost
	// it" (#1067).
	//
	// Caddy is configured entirely over the admin API, so a crash, restart
	// or binary swap reverts it to the stub Caddyfile and the layer4 app
	// disappears. The HTTP side self-heals via EnsureBaseConfig; L4 did not,
	// because its activation is guarded by "are there passthrough routes?"
	// — so a wipe that happened while the route set was empty left :443 as
	// plain HTTP indefinitely, with no PROXY/SNI passthrough and nothing to
	// notice.
	//
	// Guarded by mu: SyncNow can run concurrently with the ticker.
	l4Activated bool
}

// l4WasActivated reports whether this daemon has ever had layer4 up.
func (j *RouteSyncJob) l4WasActivated() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.l4Activated
}

// markL4Activated records that layer4 is up.
func (j *RouteSyncJob) markL4Activated() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.l4Activated = true
}

// NewRouteSyncJob creates a new route sync job
func NewRouteSyncJob(routeStore *RouteStore, proxyManager *ProxyManager, interval time.Duration) *RouteSyncJob {
	if interval <= 0 {
		interval = 5 * time.Second // default 5 seconds
	}

	return &RouteSyncJob{
		routeStore:   routeStore,
		proxyManager: proxyManager,
		interval:     interval,
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// SetL4ProxyManager sets the L4 proxy manager for TLS passthrough route sync.
func (j *RouteSyncJob) SetL4ProxyManager(l4 *L4ProxyManager) {
	j.l4ProxyManager = l4
}

// SetWakeTracker wires the wake-state tracker so the sync loop knows
// which containers are currently routed through the daemon's wake
// handler. Nil is allowed and disables the wake-coordination branch
// (the loop behaves exactly as it did before Phase 3).
func (j *RouteSyncJob) SetWakeTracker(t WakeTracker) {
	j.wakeTracker = t
}

// ProxyManager returns the underlying *ProxyManager so the wake
// wiring in DualServer can build a Router without having to pass
// proxyManager around as a separate construction parameter (it's
// already captured here).
func (j *RouteSyncJob) ProxyManager() *ProxyManager { return j.proxyManager }

// RouteStore returns the underlying *RouteStore for the same wiring
// reason as ProxyManager — DualServer composes a wake.WakeProxy from
// the same primitives the sync job already holds.
func (j *RouteSyncJob) RouteStore() *RouteStore { return j.routeStore }

// Start begins the background sync job
// It runs an immediate sync, then syncs at the configured interval
func (j *RouteSyncJob) Start(ctx context.Context) {
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return
	}
	j.running = true
	j.stopCh = make(chan struct{})
	j.doneCh = make(chan struct{})
	j.mu.Unlock()

	go j.run(ctx)
}

// Stop stops the background sync job
func (j *RouteSyncJob) Stop() {
	j.mu.Lock()
	if !j.running {
		j.mu.Unlock()
		return
	}
	j.mu.Unlock()

	close(j.stopCh)
	<-j.doneCh

	j.mu.Lock()
	j.running = false
	j.mu.Unlock()
}

// SyncNow triggers an immediate sync
func (j *RouteSyncJob) SyncNow(ctx context.Context) error {
	return j.sync(ctx)
}

// run is the main loop for the background sync job
func (j *RouteSyncJob) run(ctx context.Context) {
	defer close(j.doneCh)

	// Run initial sync immediately
	if err := j.sync(ctx); err != nil {
		log.Printf("[RouteSyncJob] Initial sync failed: %v", err)
	}

	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	for {
		select {
		case <-j.stopCh:
			log.Println("[RouteSyncJob] Stopping route sync job")
			return
		case <-ctx.Done():
			log.Println("[RouteSyncJob] Context cancelled, stopping route sync job")
			return
		case <-ticker.C:
			if err := j.sync(ctx); err != nil {
				log.Printf("[RouteSyncJob] Sync failed: %v", err)
			}
		}
	}
}

// sync performs the actual synchronization from PostgreSQL to Caddy
func (j *RouteSyncJob) sync(ctx context.Context) error {
	// Self-heal the base Caddy config first. The bundled Caddy boots from a stub
	// Caddyfile and is configured entirely over the admin API; any caddy
	// reload/restart/crash reverts the running config to that stub, wiping the
	// http app. Without this, the route diff below would loop on
	// "400 invalid traversal path" and :443 would stay dark (issue #400).
	// Rebuilding the base lets the diff repopulate HTTP routes (+ TLS subjects)
	// and re-activate layer4 on this same tick. Cheap (one GET) when intact.
	if rebuilt, err := j.proxyManager.EnsureBaseConfig(); err != nil {
		log.Printf("[RouteSyncJob] Base Caddy config reconcile failed: %v", err)
	} else if rebuilt {
		log.Printf("[RouteSyncJob] Caddy reverted to stub config — rebuilt base edge config; routes/TLS/L4 will be repopulated this sync (#400)")
		// Per-route TLS subjects come back via the diff below, but the
		// `*.<base-domain>` wildcard isn't tied to a route — re-provision it
		// here so a Caddy reload doesn't silently drop wildcard coverage (#389).
		if j.proxyManager.HasDNSChallenge() {
			if err := j.proxyManager.ProvisionWildcardTLS(); err != nil {
				log.Printf("[RouteSyncJob] re-provision wildcard TLS after rebuild failed: %v", err)
			}
		}
	}

	// Get routes from PostgreSQL (source of truth)
	dbRoutes, err := j.routeStore.List(ctx, true) // activeOnly = true
	if err != nil {
		return err
	}

	// Split routes by protocol type
	var httpGRPCRoutes []*RouteRecord
	var tlsPassthroughRoutes []*RouteRecord
	for _, r := range dbRoutes {
		if r.Protocol == string(RouteProtocolTLSPassthrough) {
			tlsPassthroughRoutes = append(tlsPassthroughRoutes, r)
		} else {
			httpGRPCRoutes = append(httpGRPCRoutes, r)
		}
	}

	// Sync HTTP/gRPC routes to ProxyManager (existing behavior)
	if err := j.syncHTTPRoutes(httpGRPCRoutes); err != nil {
		log.Printf("[RouteSyncJob] HTTP route sync error: %v", err)
	}

	// Sync TLS passthrough routes to L4ProxyManager
	if j.l4ProxyManager != nil {
		if err := j.syncL4Routes(tlsPassthroughRoutes); err != nil {
			log.Printf("[RouteSyncJob] L4 route sync error: %v", err)
		}
	}

	// Reconcile TLS automation subjects for every route that should have a
	// certificate. Deliberately AFTER syncHTTPRoutes, so routes added on this
	// tick are included, and deliberately not gated on that call succeeding —
	// the subjects of already-synced routes still need checking when one route
	// fails.
	//
	// syncHTTPRoutes only provisions TLS for routes it ADDS to Caddy; a route
	// already present takes the needsUpdate path, which never looks at TLS
	// subjects. Without this call a route whose subject went missing is never
	// repaired (#1584).
	if err := j.syncTLSSubjects(dbRoutes); err != nil {
		log.Printf("[RouteSyncJob] TLS subject reconcile error: %v", err)
	}

	return nil
}

// syncTLSSubjects ensures every active route that Caddy terminates TLS for has
// a subject in Caddy's TLS automation policies (#1584).
//
// Excludes TLS-passthrough routes: those terminate TLS at the backend, so
// Caddy must not chase a certificate for them. Excludes inactive routes for
// the same reason RemoveTLSSubject exists — no point spending ACME budget on a
// hostname nothing serves.
//
// The domain list is the union of dbRoutes and whatever Caddy's http app is
// CURRENTLY, actually serving (ListRoutes()) — not dbRoutes alone. A
// hostname can reach Caddy's http app through a route that predates or lives
// entirely outside Containarium's own route management — e.g. a
// tunnel-declared --public-aliases hostname (#1872), which is never added
// via AddRoute and so has no row here at all. Such a hostname relied
// entirely on Caddy's own built-in automatic-HTTPS subject discovery, which
// stops covering it once ActivateL4 moves the http app's server off :443 —
// silently breaking its certificate renewal with no dbRoutes-only reconciler
// able to notice or repair it (#1880). ListRoutes() reads only the http
// app's own routes, so it can never include a TLS-passthrough hostname
// (those live in the layer4 app) — no extra protocol filtering needed for
// this source. A failure reading it is logged and otherwise ignored, so a
// transient admin-API error never blocks reconciling the domains already
// known from dbRoutes.
func (j *RouteSyncJob) syncTLSSubjects(dbRoutes []*RouteRecord) error {
	if j.proxyManager == nil {
		return nil
	}

	seen := make(map[string]bool, len(dbRoutes))
	domains := make([]string, 0, len(dbRoutes))
	add := func(domain string) {
