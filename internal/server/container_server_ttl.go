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
