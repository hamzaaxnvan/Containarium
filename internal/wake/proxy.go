package wake

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/auth"
)

// WakeStarter starts a container and blocks until its primary route
// is TCP-ready, or until the wake timeout elapses. Returns ready=false
// (with no error) on timeout; returns err only on a hard failure of
// the underlying Start call.
//
// Implemented by an adapter over ContainerServer.StartContainer.
// Defined as an interface so the HTTP handler can be unit-tested
// without standing up Incus.
type WakeStarter interface {
	WakeForRequest(ctx context.Context, username string) (ready bool, containerIP string, port int, err error)
}

// RouteLookup resolves an incoming request's Host header to the
// container that owns the matching route, plus the route's direct
// target so SwapToDirect can be called after the wake completes.
//
// Returns ok=false on no match (we 404 the request).
type RouteLookup interface {
	ResolveByHost(ctx context.Context, host string) (route *app.RouteRecord, ok bool, err error)
}

// AuditLogger records each wake outcome. Same shape as
// autosleep.AuditLogger so we can reuse the existing adapter.
type AuditLogger interface {
	Log(event string, fields map[string]any)
}

// WakeProxy is the HTTP handler mounted at /wake/. When Caddy
// forwards a request to it (because the container is in wake mode),
// the handler wakes the container, then reverse-proxies the request
// through. Concurrent requests for the same container coalesce on a
// single wake — the leader runs the Start, the followers wait on a
// done channel.
type WakeProxy struct {
	starter     WakeStarter
	routeLookup RouteLookup
	router      *Router    // for fire-and-forget SwapToDirect after success
	routeStore  RouteStore // for fetching routes to pass to SwapToDirect
	audit       AuditLogger
	waitTimeout time.Duration

	// Phase 1.9 — source-IP allowlist for the wake handler.
	// Empty == permissive rollout mode (loopback always
	// accepted; everything else gated by a startup WARNING).
	trustedProxies []netip.Prefix

	inflightMu sync.Mutex
	inflight   map[string]*inflightWake // key: containerName
}

// SetTrustedProxies configures the source-IP allowlist for the
// wake handler. Pass nil/empty to keep rollout-mode permissive
// (the constructor's default). Typically wired at dual-server
// startup from LoadTrustedProxies().
func (w *WakeProxy) SetTrustedProxies(p []netip.Prefix) {
	w.trustedProxies = p
}

// RouteStore is the narrow interface WakeProxy needs to fetch a
// container's routes for the SwapToDirect callback. Satisfied by
// *app.RouteStore. Separate from RouteLookup so the proxy can do both
// lookups (one for the resolution, one for the swap-back) without
// either being too wide.
type RouteStore interface {
	ListByContainer(ctx context.Context, containerName string) ([]*app.RouteRecord, error)
}

type inflightWake struct {
	done chan struct{}
	ip   string
	port int
	err  error
}

// NewWakeProxy constructs the handler. waitTimeout defaults to 30s
// if zero or negative is passed.
func NewWakeProxy(
	starter WakeStarter,
	routeLookup RouteLookup,
	routeStore RouteStore,
	router *Router,
