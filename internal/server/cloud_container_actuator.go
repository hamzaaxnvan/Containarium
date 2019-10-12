package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/cloud"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// cloudContainerActuator implements cloud.ContainerActuator by driving the local
// Incus instance state for cloud-assigned containers (#354). Create stamps
// user.containarium.tenant=<org_id> so the #315 network-policy enforcer can match
// the container to its org's egress policy (cloud names are cld-<uuid>, not
// <tenant>-container, so the label — not the name — carries the tenant).
//
// Scope: create / start / stop / delete to converge to desired_state, plus
// exposing the container's routes at the host edge (routeStore → Caddy) and
// injecting secret_env. Not the richer create options the user-facing
// ContainerService offers — cloud-assigned boxes are minimal v1 workloads.
type cloudContainerActuator struct {
	incus  *incus.Client
	routes *app.RouteStore // nil when the host has no route store (no Postgres/Caddy) → routes skipped
}

// newCloudContainerActuator builds an actuator with its own Incus client and the
// daemon's route store (may be nil — then route exposure is skipped). Returns an
// error (→ caller runs policy-sync-only) if Incus is unavailable, e.g. non-Linux.
func newCloudContainerActuator(routes *app.RouteStore) (*cloudContainerActuator, error) {
	c, err := incus.New()
	if err != nil {
		return nil, fmt.Errorf("incus client: %w", err)
	}
	return &cloudContainerActuator{incus: c, routes: routes}, nil
}

// EnsureRunning creates the container (stamping the tenant label) if absent,
// ensures it is started, and converges its edge routes. Idempotent.
//
// Also self-heals the tenant label on every reconcile of an already-existing
// container, not just at creation (#780 follow-up) — a container created
// before this labeling logic shipped (or whose label was lost some other
// way) would otherwise stay permanently unmatched by the network-policy
// enforcer, since create() only runs once. The reconcile loop already
// carries org_id for every live assignment each cycle, so this converges the
// whole fleet within one watchPollInterval of a daemon restart, no separate
// backfill tool needed.
func (a *cloudContainerActuator) EnsureRunning(ctx context.Context, spec cloud.ContainerSpec) error {
	info, err := a.incus.GetContainer(spec.LocalName)
	if err != nil {
		// Treat any lookup failure as "not present" and try to create — a real
		// create error surfaces below; a benign not-found proceeds to create.
		if cerr := a.create(spec); cerr != nil {
			return cerr
		}
	} else {
		// Best-effort: a stamp failure here shouldn't block start/route
		// convergence for an otherwise-healthy container — it retries next cycle.
		if terr := a.ensureTenantLabel(spec.LocalName, spec.OrgID, info.Tenant); terr != nil {
			log.Printf("[cloud] stamp tenant label on %s: %v", spec.LocalName, terr)
		}
		if isRunning(info.State) {
			a.applyRoutes(ctx, spec) // already running — still converge routes
			return nil
		}
	}
	if err := a.incus.StartContainer(spec.LocalName); err != nil {
		return fmt.Errorf("start %s: %w", spec.LocalName, err)
	}
	a.applyRoutes(ctx, spec)
	return nil
}

// ensureTenantLabel stamps localName's tenant label when it doesn't already
// match orgID.
func (a *cloudContainerActuator) ensureTenantLabel(localName, orgID, currentTenant string) error {
	if !tenantLabelStale(orgID, currentTenant) {
		return nil
	}
	return a.incus.SetConfig(localName, incus.TenantLabelKey, orgID)
}
