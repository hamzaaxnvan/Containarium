package cloud

// REST transport for the cloud actuation channel (OSS #722).
//
// The gRPC transport (client.go) only works against a control plane that
// serves native gRPC. A managed cloud (e.g. cloud.containarium.dev) often
// fronts the API as REST/grpc-gateway ONLY — native gRPC over :443 there
// returns 403/text-html. A BYO-compute host enrolled against such a control
// plane therefore couldn't enroll, heartbeat, or report its capability, which
// left the host's capacity "unknown" in the operator view and made the
// host-sweeper treat the never-heartbeating host as stranded.
//
// This transport speaks the SAME actuation RPCs over their grpc-gateway REST
// mappings (POST /v1/actuation/{heartbeat,status,enroll}), carrying the host
// bearer as the `Grpc-Metadata-Host-Bearer` header — grpc-gateway strips the
// `Grpc-Metadata-` prefix and forwards it as the `host-bearer` metadata the
// HostBearerInterceptor authenticates on. Requests/responses are the OSS proto
// types via protojson, so the wire matches the gateway by construction.
//
// WatchAssignments (server-streaming) is NOT implemented here: a BYOC host is
// push-driven (the cloud drives it via the sentinel peer-proxy), so it has no
// assignments to pull. REST mode therefore runs heartbeat + status only.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	cloudv1 "github.com/footprintai/containarium/pkg/pb/containarium/cloud/v1"
)

// isRESTControlPlane reports whether the control-plane address is an http(s)
// URL (REST/grpc-gateway) rather than a gRPC host:port. An https:// or http://
// prefix selects the REST transport.
func isRESTControlPlane(controlPlane string) bool {
	cp := strings.TrimSpace(controlPlane)
	return strings.HasPrefix(cp, "https://") || strings.HasPrefix(cp, "http://")
}

// restActuation implements the unary actuation RPCs (Heartbeat, ReportHostStatus)
// over grpc-gateway REST. It satisfies the unaryActuation interface the client
// uses; the gRPC CallOption args are accepted and ignored.
type restActuation struct {
	base   string // control-plane base URL, no trailing slash
	bearer string // host bearer, sent as Grpc-Metadata-Host-Bearer (empty for enroll, which self-auths via the join token)
	hc     *http.Client

	// mu guards consecutive5xx. Heartbeat, status and trust-bundle calls
	// can run concurrently on one client.
	mu             sync.Mutex
	consecutive5xx int
	recyclePending bool // set at the threshold; honoured at the start of the next request

	// Bounded connection lifetime (#3113). hc is the current client; once its
	// transport is older than maxTransportAge, the next request swaps in a
	// fresh clone and retires the old client. Retired clients keep serving
	// their in-flight requests and are closed once drained. All guarded by mu.
	now           func() time.Time // injectable clock
	transportBorn time.Time
	inflight      map[*http.Client]int
	retired       []*retiredClient
}

// retiredClient is a swapped-out client waiting to drain.
type retiredClient struct {
	hc *http.Client
	// idleSwept is set when a sweep found it with no in-flight request and
	// closed its idle connections; a second such sweep drops it. Two passes
	// because an HTTP/2 stream is released asynchronously after its body is
	// closed, so the first close can find the connection still busy.
	idleSwept bool
}
