package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/internal/sandbox/pool"
	"github.com/footprintai/containarium/internal/sandbox/ratelimit"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// sandboxKindLabelSuffix marks an Incus instance as an ephemeral sandbox
// (SandboxService), distinct from a persistent tenant box. Listing/reaping
// code that iterates ContainerService's tenant boxes should skip instances
// carrying this label — not yet wired in Phase 1, since sandbox names
// (sandbox-<id>) don't collide with the <username>-container convention
// those paths key on, but worth flagging for whoever adds a sandbox-aware
// sweep.
const sandboxKindLabelSuffix = "kind"

// SandboxKindLabelKey is the full Incus config key
// (incus.LabelPrefix-qualified) written via ContainerConfig.ExtraConfig at
// spawn time.
const SandboxKindLabelKey = incus.LabelPrefix + sandboxKindLabelSuffix

// SandboxKindLabelValue is the label's value on every sandbox.
const SandboxKindLabelValue = "sandbox"

// sandboxNamePrefix names every sandbox's Incus container: sandbox-<12 hex
// chars>. The sandbox_id IS the Incus container name — no separate mapping
// table, matching how a tenant's container name IS its box.BoxRef.Tenant +
// "-container" elsewhere in this codebase.
const sandboxNamePrefix = "sandbox-"

// defaultSandboxIdleTTL applies when SpawnSandboxRequest.idle_ttl_seconds is
// unset (0). Enforced by the ttlsweeper.Manager wired in dual_server.go
// (#1488 Phase 4, via ttlsweeperSandboxAdapter and SandboxServer's own
// DeleteContainer method in sandbox_ttlsweeper_adapter.go) — expires_at
// is reported so a caller can see the horizon its sandbox will actually
// be reaped against.
const defaultSandboxIdleTTL = 30 * time.Minute

// spawnNetworkTimeout bounds how long SpawnSandbox waits for the guest's
// network to come up. Same bound container.Manager.Create uses for the
// equivalent wait.
const spawnNetworkTimeout = 30 * time.Second

// SandboxServer implements SandboxService: the two-digit-ms spawn path. A
// sandbox has no per-tenant Linux account and no SSH — these five RPCs are
// its entire access surface.
//
// Talks to incus.Backend directly rather than going through
// pkg/core/container.Manager: Manager.Create is the full persistent-box
// identity flow (jump-server account, in-guest user, SSH keys) that
// sandboxes are explicitly scoped to skip. Reusing it would mean threading
// a "skip everything" option through a function whose entire shape is that
// provisioning.
//
// pool is optional (#1488 Phase 3). nil means every spawn takes the cold
// path — today's exact Phase 1 behavior, including for a caller that never
// sets allow_cold_start (its zero value is false): admission control
// (RESOURCE_EXHAUSTED on an exhausted pool) only ever applies when a pool
// is actually configured. A configured-but-untargeted pool (no ready
// member for a requested template) is indistinguishable from "no pool at
// all" from SpawnSandbox's perspective — both fall through to cold,
// subject to the same allow_cold_start gate.
type SandboxServer struct {
	pb.UnimplementedSandboxServiceServer
	incus incus.Backend
	pool  *pool.Pool

	// rateLimiter gates SpawnSandbox per-tenant (#1488 Phase 4). Always
	// non-nil after NewSandboxServer (ratelimit.Disabled(): every spawn
	// allowed) — set via SetRateLimiter to actually enforce a limit, so
	// SpawnSandbox never needs its own nil check here.
	rateLimiter *ratelimit.Limiter

	// claimed tracks, per pool-claimed sandbox_id, the pool.Member Claim
	// returned — DeleteSandbox needs it to route to pool.Release (which
	// also frees the member's IPAM address) instead of destroy() (which
	// doesn't know about IPAM at all; a cold-path sandbox never has an
	// address to free). In-memory only: like container_server.go's own
	// pendingCreations, a claimed-but-not-yet-deleted sandbox's tracking
	// doesn't survive a daemon restart — no different in kind from every
	// other piece of this daemon's in-flight state, and sandboxes are
	// short-lived by design.
	claimedMu sync.Mutex
	claimed   map[string]*pool.Member
}

// NewSandboxServer constructs a SandboxServer over the given Incus backend.
// p is optional; nil disables pool-backed spawns (see the type doc). The
// spawn rate limiter starts disabled (ratelimit.Disabled()) — call
// SetRateLimiter to enable one.
func NewSandboxServer(backend incus.Backend, p *pool.Pool) *SandboxServer {
	return &SandboxServer{
		incus:       backend,
		pool:        p,
		rateLimiter: ratelimit.Disabled(),
		claimed:     make(map[string]*pool.Member),
	}
}

// SetRateLimiter installs a per-tenant spawn rate limiter (#1488 Phase
// 4). Optional — call after NewSandboxServer; passing nil restores
// ratelimit.Disabled() rather than leaving a real nil pointer around,
// mirroring ClusterServer.SetCaps's own optional-config-after-
// construction shape.
func (s *SandboxServer) SetRateLimiter(l *ratelimit.Limiter) {
	if l == nil {
		l = ratelimit.Disabled()
	}
	s.rateLimiter = l
}

// StartReconciler runs pool.Reconcile on interval until ctx is done. No-op
// if s.pool is nil (no pool configured). Same ticker shape as this
// package's other background loops (see startIntegrityHeartbeat in
// dual_server.go): one immediate reconcile so a freshly-started daemon
// doesn't wait a full interval before warming its first members, then on
// the cadence.
func (s *SandboxServer) StartReconciler(ctx context.Context, interval time.Duration) {
	if s.pool == nil {
		return
	}

	s.pool.Reconcile(ctx)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.pool.Reconcile(ctx)
			}
		}
	}()
}

// newSandboxID generates a sandbox_id / Incus container name: sandbox- plus
// 12 hex characters (6 random bytes) — collision odds low enough that a
// create-time name clash is not worth handling as anything other than the
// CreateContainer error it would surface.
func newSandboxID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate sandbox id: %w", err)
	}
	return sandboxNamePrefix + hex.EncodeToString(b), nil
}

// resolveTemplate maps a requested template to its guest image.
// SANDBOX_TEMPLATE_UNSPECIFIED resolves to BASE, mirroring
// ostype.ImageForOSType's unspecified-defaults-to-Ubuntu convention. Any
// other value is rejected: v1 ships BASE only (see the proto's doc comment
// on SandboxTemplate), and silently falling back would let a caller depend
// on a template that doesn't actually exist yet.
func resolveTemplate(t pb.SandboxTemplate) (resolved pb.SandboxTemplate, image string, err error) {
	switch t {
	case pb.SandboxTemplate_SANDBOX_TEMPLATE_UNSPECIFIED, pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE:
		return pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE, "images:ubuntu/24.04", nil
	default:
		return t, "", status.Errorf(codes.InvalidArgument, "unsupported sandbox template %v (v1 ships SANDBOX_TEMPLATE_BASE only)", t)
	}
}

// destroy stops (force, errors ignored — deleting a stopped or a still-
// running instance both work) then deletes name, returning the delete
// error. Same shape as pkg/core/container.Manager.cleanup, duplicated
// rather than shared because SandboxServer deliberately does not depend on
// container.Manager (see the type doc). A spawn-failure rollback discards
// the returned error (the spawn already failed); DeleteSandbox propagates
// it — a caller asking to delete needs to know if that didn't happen.
func (s *SandboxServer) destroy(name string) error {
	_ = s.incus.StopContainer(name, true)
	return s.incus.DeleteContainer(name)
}

// SpawnSandbox tries the warm pool first (#1488 Phase 3, served_from =
// POOL) when one is configured, and falls back to the cold path
// (served_from = COLD) otherwise: pool disabled, or the requested
// template's ready ring is empty and the caller set allow_cold_start. An
// exhausted pool with allow_cold_start unset (false) returns
// RESOURCE_EXHAUSTED instead of silently paying the cold path's cost — a
// latency SLO whose slow path is invisible isn't an SLO.
//
// Per-tenant rate limiting (#1488 Phase 4) is checked immediately after
// authentication, before template resolution or any pool/cold work — a
// throttled caller shouldn't pay (or cause) any of that. It gates every
// spawn regardless of path: a caller hammering SpawnSandbox is exactly as
// disruptive to shared pool capacity via a fast POOL claim loop as via
// slow COLD creates, so this isn't scoped to one path or the other.
func (s *SandboxServer) SpawnSandbox(ctx context.Context, req *pb.SpawnSandboxRequest) (*pb.SpawnSandboxResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSandboxesWrite); err != nil {
		return nil, err
	}
	subject, _, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated subject")
	}

	if !s.rateLimiter.Allow(subject) {
		if err := s.rateLimiter.Err(); err != nil {
			return nil, status.Errorf(codes.Internal, "sandbox spawn rate limiting misconfigured: %v", err)
		}
		return nil, status.Errorf(codes.ResourceExhausted, "spawn rate limit exceeded for %s; retry after a short backoff", subject)
	}

	template, image, err := resolveTemplate(req.Template)
	if err != nil {
		return nil, err
	}

	if s.pool != nil {
		resp, err := s.claimFromPool(template, subject, req)
		switch {
		case err == nil:
			return resp, nil
		case !errors.Is(err, pool.ErrPoolExhausted):
			// A member was actually claimed (removed from the ring) but
			// setup failed after that — a real error, not "no pool
			// member available." claimFromPool already unwound it.
			return nil, err
		case !req.AllowColdStart:
			return nil, status.Errorf(codes.ResourceExhausted,
				"no warm %s sandbox available; retry, or set allow_cold_start=true for the slower cold-create path", template)
		}
		// Exhausted, but the caller opted into the cold path — fall
		// through to it below, exactly like a nil pool would.
	}

	id, err := newSandboxID()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	ttl := sandboxTTL(req.IdleTtlSeconds)
	now := time.Now()

	extraConfig := qualifyLabels(sandboxLabelSuffixes(template, ttl))
	extraConfig[incus.TenantLabelKey] = subject
	// Stamp the exact key/format ttlsweeper.Decide reads (see
	// stampTTL's doc comment in container_server_ttl.go) via
	// ContainerConfig.ExtraConfig rather than a follow-up SetConfig
	// call — one fewer Incus round trip on the cold path, which is
	// the path this design note is trying to make fast.
	extraConfig[incus.TTLExpiresAtKey] = now.Add(ttl).UTC().Format(time.RFC3339)
	config := incus.ContainerConfig{
		Name:        id,
		Image:       image,
		ExtraConfig: extraConfig,
	}

	if err := s.incus.CreateContainer(config); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create sandbox: %v", err)
	}
	if err := s.incus.StartContainer(id); err != nil {
		_ = s.incus.DeleteContainer(id)
		return nil, status.Errorf(codes.Internal, "failed to start sandbox: %v", err)
	}
	if _, err := s.incus.WaitForNetwork(id, spawnNetworkTimeout); err != nil {
		_ = s.destroy(id)
		return nil, status.Errorf(codes.Internal, "sandbox network did not come up: %v", err)
	}

	return &pb.SpawnSandboxResponse{
		Sandbox: &pb.Sandbox{
			SandboxId:  id,
			State:      pb.SandboxState_SANDBOX_STATE_RUNNING,
			Template:   template,
			ServedFrom: pb.ServedFrom_SERVED_FROM_COLD,
			CreatedAt:  timestamppb.New(now),
			ExpiresAt:  timestamppb.New(now.Add(ttl)),
		},
	}, nil
}

// sandboxTTL resolves a request's idle_ttl_seconds to a concrete duration,
// applying defaultSandboxIdleTTL when unset (<= 0).
