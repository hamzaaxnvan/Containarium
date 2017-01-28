package server

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// maxTTLSeconds caps the duration a caller can request. 604800 seconds
// (7 days) mirrors the 168h ceiling enforced by `containarium ttl set`
// in PR #297 and the proto comment on SetContainerTTLRequest. Larger
// values return InvalidArgument so callers see a clear error rather
// than a silently clamped value — matches the CLI's behavior on the
// same input. Centralized here so the cap is enforced both by the CLI
// (before the round trip, friendly UX) and by the server (defense in
// depth, in case some other client forgets).
const maxTTLSeconds int64 = 7 * 24 * 60 * 60

// SetContainerTTL schedules or clears a container's auto-delete time.
// duration_seconds == 0 clears any existing TTL (the container persists
// indefinitely). duration_seconds > 0 sets ttl_expires_at to now() +
// duration. Capped at maxTTLSeconds; larger values return
// InvalidArgument. Persistence model: the wall-clock expiry is stamped
// onto the Incus container config under user.containarium.ttl_expires_at
// (RFC3339), so it survives daemon restart without a separate store.
// Read by the ttlsweeper goroutine on every tick (PR #299) and by
// toProtoContainer on the list/get read paths so callers see the
// committed value.
//
// Mirrors the username-as-name convention of the other per-container
// RPCs (ToggleAutoSleep, StopContainer, ...): req.Name carries the
// username, the handler resolves <username>-container under the hood
// via manager.Get. Consistency matters because the CLI in PR #297
// passes the bare username and the gRPC stubs flow that value into
// req.Name verbatim.
func (s *ContainerServer) SetContainerTTL(ctx context.Context, req *pb.SetContainerTTLRequest) (*pb.SetContainerTTLResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersWrite); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if err := validateTTLSeconds(req.DurationSeconds); err != nil {
		return nil, err
	}

	// Treat req.Name as the bare username (matches the per-container
	// RPC convention; see CLI PR #297 which sends the bare username
	// through ttlClientSet). manager.Get appends "-container".
	username := req.Name
	if err := auth.AuthorizeTenant(ctx, username); err != nil {
		return nil, err
	}

	info, err := s.boxes().Get(ctx, box.BoxRef{Tenant: username})
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "container for user %s not found: %v", username, err)
	}
	if info == nil {
		return nil, status.Errorf(codes.NotFound, "container for user %s not found", username)
	}
	if info.IsCore {
		return nil, status.Errorf(codes.InvalidArgument, "container %s is a core container; TTL is for user containers only", info.Ref.Name)
	}
	containerName := info.Ref.Name

	resp := &pb.SetContainerTTLResponse{}

	// TTL-capable backends (K8s: agent-sandbox spec.shutdownTime) persist the
	// expiry natively; the Incus-config path below is the LXC persistence.
	if tc, ok := s.boxes().(box.TTLCapable); ok {
		ref := box.BoxRef{Tenant: username}
		if req.DurationSeconds == 0 {
			if err := tc.SetTTL(ctx, ref, nil); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to clear ttl: %v", err)
			}
			log.Printf("[ttl] cleared container=%s", containerName)
			return resp, nil
		}
		expiresAt := time.Now().Add(time.Duration(req.DurationSeconds) * time.Second).UTC()
		if err := tc.SetTTL(ctx, ref, &expiresAt); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to set ttl: %v", err)
		}
		log.Printf("[ttl] set container=%s expires_at=%s (duration=%ds)", containerName, expiresAt.Format(time.RFC3339), req.DurationSeconds)
		resp.TtlExpiresAt = timestamppb.New(expiresAt)
		return resp, nil
	}

	if req.DurationSeconds == 0 {
		// Clear: remove the key entirely so parseTTLExpiresAt and the
		// sweeper see "absent" rather than "empty string". UnsetConfig
		// is idempotent — clearing an already-clear TTL is a no-op
		// followed by a no-op response (TtlExpiresAt zero-value).
		if err := s.manager.UnsetConfig(containerName, incus.TTLExpiresAtKey); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to clear %s: %v", incus.TTLExpiresAtKey, err)
		}
		log.Printf("[ttl] cleared container=%s", containerName)
		return resp, nil
	}

	// Set: stamp now() + duration in UTC RFC3339 so the sweeper's
	// time.Parse round-trips with the same precision. Capping is
	// already enforced above.
	expiresAt, err := s.stampTTL(containerName, req.DurationSeconds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set %s: %v", incus.TTLExpiresAtKey, err)
	}
	log.Printf("[ttl] set container=%s expires_at=%s (duration=%ds)", containerName, expiresAt.Format(time.RFC3339), req.DurationSeconds)
	resp.TtlExpiresAt = timestamppb.New(expiresAt)
	return resp, nil
}

// validateTTLSeconds checks a requested TTL duration against the bounds
// shared by SetContainerTTL and the birth-TTL path in CreateContainer (#523).
// Zero is always valid — it means "no TTL" on create and "clear the TTL" on
// set, so callers handle the zero case themselves. Negative and over-cap
// (> maxTTLSeconds, 7 days) return InvalidArgument so the two entry points
// reject identical input identically.
func validateTTLSeconds(seconds int64) error {
	if seconds < 0 {
