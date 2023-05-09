package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// migrationDefaults clamps the request to sane ranges and supplies
// defaults for unset fields. Centralizes the policy so the actual
// orchestrator doesn't muddy itself with validation noise.
type migrationParams struct {
	maxIterations         int
	deltaThresholdSeconds int
	stateful              bool
}

func migrationDefaults(req *pb.MoveContainerRequest) migrationParams {
	p := migrationParams{
		maxIterations:         int(req.MaxIterations),
		deltaThresholdSeconds: int(req.DeltaThresholdSeconds),
		stateful:              req.Stateful,
	}
	if p.maxIterations < 0 {
		p.maxIterations = 0
	}
	if p.maxIterations > 10 {
		p.maxIterations = 10
	}
	if p.maxIterations == 0 && req.MaxIterations == 0 {
		// Caller didn't set it; 3 is the sweet spot for typical workloads.
		p.maxIterations = 3
	}
	if p.deltaThresholdSeconds < 1 {
		p.deltaThresholdSeconds = 5
	}
	if p.deltaThresholdSeconds > 60 {
		p.deltaThresholdSeconds = 60
	}
	return p
}

// MoveContainer migrates a container from this daemon to a peer daemon
// using pre-copy + delta-refresh. See proto/containarium/v1/service.proto
// for the high-level contract; the implementation here is the three-
// phase orchestration:
//
//	Phase 1 — initial full copy (source running)
//	  snapshot sync0 → incus copy <c>/sync0 <remote>:<c> --instance-only
//
//	Phase 2 — iterative deltas (still running)
//	  for i in 1..max_iterations:
//	      snapshot sync<i> → incus copy --refresh
//	      if elapsed < delta_threshold_seconds: break
//
//	Phase 3 — cutover (source down for the final delta)
//	  stop source
//	  snapshot final → incus copy --refresh
//	  adopt-on-target (registers host user, returns new container IP)
//	  update route store: target_ip swap
//	  cascade-cleanup on source (delete LXC, host user, etc.)
//
// Failure handling:
//   - any failure before cutover: source container is still running and
//     unchanged. Leftover sync snapshots are cleaned up best-effort.
//   - failure after `stop` but before route swap: we restart the source
//     container so the public hostname recovers — the migration is
//     aborted and the operator can retry.
//   - failure after route swap (very unlikely — just a DB update): the
//     migration is considered complete; the cascade cleanup runs and any
//     residual error is logged but doesn't fail the response.
func (s *ContainerServer) MoveContainer(ctx context.Context, req *pb.MoveContainerRequest) (*pb.MoveContainerResponse, error) {
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if req.TargetBackendId == "" {
		return nil, fmt.Errorf("target_backend_id is required")
	}
	// Admin-only: cross-backend migration is an infrastructure
	// operation, not a tenant operation. A user owning a container
	// has no legitimate need to relocate it across the fleet — that's
	// an operator-driven concern (capacity balancing, drains, GPU
	// scheduling). AuthorizeTenant alone would still let a tenant
	// move their own container; require admin on top.
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if err := auth.AuthorizeTenant(ctx, req.Username); err != nil {
		return nil, err
	}
	if s.peerPool == nil {
		return nil, fmt.Errorf("peer pool not configured; multi-backend support is disabled on this daemon")
	}
	if req.TargetBackendId == s.peerPool.LocalBackendID() {
		return nil, fmt.Errorf("cannot move container to its own backend (target_backend_id matches local)")
	}
	if s.moveRunner == nil {
		return nil, fmt.Errorf("migration runner not configured; daemon was built without incus migration support")
	}

	targetPeer := s.peerPool.Get(req.TargetBackendId)
	if targetPeer == nil {
		return nil, fmt.Errorf("target backend %q not found in peer pool", req.TargetBackendId)
	}

	containerName := fmt.Sprintf("%s-container", req.Username)

	// Each daemon's incusd knows the others as remotes via `incus
	// remote add`. The convention we use: remote name == peer backend
	// ID. Operators set this up at provisioning time. Auto-bootstrap
	// (daemon-coordinated cert exchange) is a follow-up.
	targetRemote := req.TargetBackendId
	if has, err := s.moveRunner.HasRemote(targetRemote); err != nil {
		return nil, fmt.Errorf("check incus remote: %w", err)
	} else if !has {
		return nil, fmt.Errorf("incus remote %q is not configured on this host; run `incus remote add %s <url>` first", targetRemote, targetRemote)
	}

	// Pre-flight for an encrypted container (#1360), BEFORE anything is
	// snapshotted or copied.
	//
	// The destination is the only party that can answer either half: whether
	// its key custody resolves this container's KeyRef, and which storage
	// pool is sourced at the tenant's encryptionroot. Asking after the copy
	// would mean spending a full data transfer to discover the container
	// cannot be unlocked on the far side (design §5).
	//
	// An unencrypted container skips this entirely — no extra round trip and
	// no new failure mode on the path every container takes today.
	var encStoragePool, encRefJSON string
	ref, srcPool, encrypted, err := s.encryption.migrationRef(containerName)
	if err != nil {
		return nil, fmt.Errorf("resolve encryption state for %s: %w", containerName, err)
	}
	// Pool headroom floor (#3128): every sync point below is a snapshot on
	// the source pool. srcPool is "" (the default pool) when unencrypted.
	if err := s.admitPoolHeadroom(srcPool, poolcapacity.OperationSnapshot); err != nil {
		return nil, err
	}
	if encrypted {
		refJSON, merr := json.Marshal(ref)
		if merr != nil {
			return nil, fmt.Errorf("encode the key ref for %s: %w", containerName, merr)
		}
		encRefJSON = string(refJSON)
		tenant := tenantFromRef(ref, containerName)
		prep, perr := prepareFn(targetPeer, extractAuthToken(ctx), &pb.PrepareEncryptedMigrationRequest{
			Username: req.Username,
			Tenant:   tenant,
			KeyRef:   string(refJSON),
		})
		if perr != nil {
			return nil, fmt.Errorf("pre-flight with backend %q: %w", req.TargetBackendId, perr)
		}
		if !prep.GetCanResolve() {
			return nil, status.Errorf(codes.FailedPrecondition,
				"backend %q cannot take encrypted container %s: %s. Nothing was copied",
				req.TargetBackendId, containerName, prep.GetReason())
		}
		// Every copy below targets this pool. Without it the container lands
		// on the destination's default pool — outside the tenant's
		// encryptionroot, unencrypted — while the pre-flight just said the
		// migration was safe, which is the more dangerous of the two.
		encStoragePool = prep.GetStoragePool()
		if encStoragePool == "" {
			return nil, status.Errorf(codes.FailedPrecondition,
				"backend %q accepted encrypted container %s but named no storage pool to copy it "+
					"into; refusing rather than landing it outside its tenant's encryptionroot",
				req.TargetBackendId, containerName)
		}
		log.Printf("[move] pre-flight ok: %s (tenant %s) lands on %s's storage pool %s",
			containerName, tenant, req.TargetBackendId, encStoragePool)
	}

	params := migrationDefaults(req)

	// Track iterations to surface in the response — useful for
	// operators tuning max_iterations on subsequent moves of similar
	// workloads.
	iterations := int32(0)

	// Snapshot names we'll create. Numbered so retries of a stuck
	// migration don't collide (the underlying CLI is idempotent on
	// "snapshot already exists", but we still want unique-per-iteration
	// names so each delta is referenced precisely).
	createdSnaps := []string{}
	cleanupSnaps := func() {
		for _, s2 := range createdSnaps {
			if err := s.moveRunner.DeleteSnapshot(containerName, s2); err != nil {
				log.Printf("[move] cleanup snapshot %s/%s: %v (ignored)", containerName, s2, err)
			}
		}
	}

	// PHASE 1 — initial copy.
	syncName := "containarium-move-sync0"
	if err := s.moveRunner.Snapshot(containerName, syncName); err != nil {
		return nil, fmt.Errorf("phase 1: snapshot: %w", err)
	}
	createdSnaps = append(createdSnaps, syncName)

	log.Printf("[move] phase 1: initial copy of %s/%s to %s ...", containerName, syncName, targetRemote)
	if err := s.moveRunner.CopyInitial(containerName, syncName, targetRemote, encStoragePool); err != nil {
		cleanupSnaps()
		return nil, fmt.Errorf("phase 1: initial copy: %w", err)
	}
	iterations++

	// PHASE 2 — iterative deltas, source still running.
	for i := 1; i <= params.maxIterations; i++ {
		syncName = fmt.Sprintf("containarium-move-sync%d", i)
		start := time.Now()

