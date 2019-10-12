package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/mtls"
	"github.com/footprintai/containarium/internal/policyreadiness"
	"github.com/footprintai/containarium/internal/releases"
	"github.com/footprintai/containarium/internal/security"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/footprintai/containarium/pkg/version"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rs/cors"
	_ "google.golang.org/genproto/googleapis/rpc/errdetails" // registers the detail types ErrorHandler renders
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// GatewayServer implements the HTTP/REST gateway for the gRPC service
type GatewayServer struct {
	grpcAddress            string
	httpPort               int
	authMiddleware         *auth.AuthMiddleware
	swaggerDir             string
	certsDir               string                                          // Optional: for mTLS connection to gRPC server
	internalDialer         func(context.Context, string) (net.Conn, error) // in-process transport to the gRPC server (preferred)
	startupListener        *StartupListener                                // HTTP port bound before the startup waits (#3130); nil = bind in Start
	readinessCheck         policyreadiness.CheckFunc                       // in-process core Postgres startup readiness (#3044)
	caddyCertDir           string                                          // Optional: Caddy certificate directory for /certs endpoint
	grafanaBackendURL      string                                          // Optional: internal Grafana URL for reverse proxy (e.g., "http://10.0.3.229:3000")
	alertmanagerBackendURL string                                          // Optional: internal Alertmanager URL for reverse proxy (e.g., "http://10.0.3.229:9093")
	securityStore          *security.Store                                 // Optional: for CSV export endpoint
	auditStore             *audit.Store                                    // Optional: for HTTP audit middleware
	terminalHandler        *TerminalHandler
	consoleHandler         *ConsoleHandler
	labelHandler           *LabelHandler
	eventHandler           *EventHandler
	coreServicesHandler    *CoreServicesHandler

	// Guacamole reverse proxy (browser-based RDP for Windows VMs)
	guacamoleBackendURL string

	// Backends handler (for multi-backend support, set externally)
	backendsHandler http.HandlerFunc

	// Model-gateway handler (#674). Set externally from dual_server when the
	// daemon holds a provider API key. Mounted at /v1/model/ and NOT wrapped by
	// the JWT auth middleware — it authenticates boxes with its OWN scoped
	// gateway token (modelgateway.VerifyToken) and injects the real provider key.
	modelGatewayHandler http.Handler

	// sentinelAuthSecret is the shared HMAC secret used by the
	// sentinel to authenticate calls to /authorized-keys and /certs.
	// Set via CONTAINARIUM_SENTINEL_AUTH_SECRET. Nil/short means the
	// wrapped endpoints fail closed (401) — fail-open is never an
	// acceptable default for these endpoints (see audit C-CRIT-4 and
	// A-CRIT-4).
	sentinelAuthSecret []byte

	// sentinelPubKey is the sentinel's ed25519 PUBLIC key (#688). When
	// set (CONTAINARIUM_SENTINEL_PUBLIC_KEY) the sentinel endpoints
	// accept ed25519-signed requests in addition to (or instead of) the
	// legacy HMAC. A daemon holding only this public key can verify the
	// sentinel but cannot forge a request — the BYOC-safe configuration.
	sentinelPubKey ed25519.PublicKey

	// sshAuthzTokenFallback lets /v1/ssh/authorize also accept a JWT
	// carrying ssh:authorize, for the jump host's AuthorizedKeysCommand
	// (#2527). Off by default: the route stays sentinel-only.
	sshAuthzTokenFallback bool

	// containerExistsFn is the orphan filter used by
	// /authorized-keys (#343). When set, the handler drops users
	// whose container has been deleted but whose home dir survived.
	// Wired from dual_server using the container manager.
	containerExistsFn func(username string) bool

	// trustBundleProvider feeds /authorized-keys' cached SSH CA trust
	// bundle (#1928). nil = this daemon has no cloud client enrolled, so
	// the response never carries one. Wired from dual_server to
	// cloud.Client.SSHTrustedUserCAKeys when the host is cloud-enrolled.
	trustBundleProvider TrustBundleProvider

	// authorizedKeysHandler overrides the default home-dir-walking
	// /authorized-keys handler. The K8s runtime installs a lister-backed
	// handler here (boxes have no /home on the node). nil = default.
	authorizedKeysHandler http.HandlerFunc

	// sentinelKeyHandler overrides the default /authorized-keys/sentinel
	// handler. The K8s runtime installs a Pipe-authorizing handler (the
	// sentinel key belongs at the node gateway, not in box home dirs).
	// nil = default.
	sentinelKeyHandler http.HandlerFunc

	// Wake handler (for serverless / wake-on-HTTP, set externally).
	// Mounted at /wake/ and at the root catch-all when the daemon's
	// wake feature is enabled. NOT wrapped by the JWT auth middleware
	// — Caddy forwards user traffic here, and that traffic doesn't
	// carry the daemon's JWT.
	wakeHandler http.Handler

	// Alert relay (no auth — internal network only)
	alertRelayMu     sync.RWMutex
	alertRelayURL    string // external webhook URL to forward to
	alertRelaySecret string // HMAC-SHA256 signing secret

	// Callback to record relay delivery attempts (set by dual_server)
	recordDeliveryFn func(ctx context.Context, alertName, source, webhookURL string, success bool, httpStatus int, errMsg string, payloadSize, durationMs int)
}

// authorizeTenantWSAccess reports whether claims may access requestedUsername's
// resource over a WebSocket upgrade: the subject must own the tenant or hold
// the admin role. Mirrors auth.AuthorizeTenant's decision exactly, adapted
// for an already-validated *auth.Claims (the gRPC-context form
// AuthorizeTenant reads doesn't exist on this HTTP/WebSocket upgrade path).
// Shared by both the /terminal and /console-attach WebSocket routes below —
// the two per-tenant WebSocket surfaces this gateway serves.
func authorizeTenantWSAccess(claims *auth.Claims, requestedUsername string) error {
	if claims == nil {
		return fmt.Errorf("no authenticated subject")
	}
	if auth.HasRole(claims.Roles, auth.RoleAdmin) {
		return nil
	}
	if claims.Username != requestedUsername {
		return fmt.Errorf("not authorized for this tenant")
	}
	return nil
}

// NewGatewayServer creates a new gateway server
func NewGatewayServer(grpcAddress string, httpPort int, authMiddleware *auth.AuthMiddleware, swaggerDir string, certsDir string, caddyCertDir string) *GatewayServer {
	// Try to create terminal handler (may fail if Incus not available)
	terminalHandler, err := NewTerminalHandler()
	if err != nil {
		log.Printf("Warning: Terminal handler not available: %v", err)
	}

	// Try to create console handler (may fail if Incus not available)
	consoleHandler, err := NewConsoleHandler()
	if err != nil {
		log.Printf("Warning: Console handler not available: %v", err)
	}

	// Try to create label handler (may fail if Incus not available)
	labelHandler, err := NewLabelHandler()
	if err != nil {
		log.Printf("Warning: Label handler not available: %v", err)
	}

	// Try to create core services handler (may fail if Incus not available)
	coreServicesHandler, err := NewCoreServicesHandler()
	if err != nil {
		log.Printf("Warning: Core services handler not available: %v", err)
	}

	// Create event handler with global event bus
	eventHandler := NewEventHandler(events.GetBus())

	return &GatewayServer{
		grpcAddress:         grpcAddress,
		httpPort:            httpPort,
		authMiddleware:      authMiddleware,
		swaggerDir:          swaggerDir,
		certsDir:            certsDir,
		caddyCertDir:        caddyCertDir,
		terminalHandler:     terminalHandler,
		consoleHandler:      consoleHandler,
		labelHandler:        labelHandler,
		eventHandler:        eventHandler,
		coreServicesHandler: coreServicesHandler,
	}
}

// SetInternalDialer makes the gateway reach the gRPC server through dial (the
// daemon's in-process listener) instead of a network address. That transport is
// what tells the gRPC server this caller is the gateway and may forward
// JWT-verified identity metadata.
func (gs *GatewayServer) SetInternalDialer(dial func(context.Context, string) (net.Conn, error)) {
	gs.internalDialer = dial
}

// SetStartupListener makes Start serve on sl, the HTTP port the daemon bound
// before its startup waits (#3130), instead of binding the port itself.
func (gs *GatewayServer) SetStartupListener(sl *StartupListener) {
	gs.startupListener = sl
}

// SetGrafanaBackendURL sets the internal Grafana URL for the reverse proxy
func (gs *GatewayServer) SetGrafanaBackendURL(backendURL string) {
	gs.grafanaBackendURL = backendURL
}

// SetAlertmanagerBackendURL sets the internal Alertmanager URL for the reverse proxy
func (gs *GatewayServer) SetAlertmanagerBackendURL(backendURL string) {
	gs.alertmanagerBackendURL = backendURL
}

// SetGuacamoleBackendURL sets the internal Guacamole URL for the reverse proxy
func (gs *GatewayServer) SetGuacamoleBackendURL(backendURL string) {
	gs.guacamoleBackendURL = backendURL
}

// SetSecurityStore sets the security store for the CSV export endpoint
func (gs *GatewayServer) SetSecurityStore(store *security.Store) {
	gs.securityStore = store
}

// SetAuditStore sets the audit store for the HTTP audit middleware
func (gs *GatewayServer) SetAuditStore(store *audit.Store) {
	gs.auditStore = store
}

// SetRecordDeliveryFn sets the callback used to record relay delivery attempts
func (gs *GatewayServer) SetRecordDeliveryFn(fn func(ctx context.Context, alertName, source, webhookURL string, success bool, httpStatus int, errMsg string, payloadSize, durationMs int)) {
	gs.recordDeliveryFn = fn
}

// SetAlertRelayConfig sets the external webhook URL and HMAC signing secret
// for the alert relay handler. Thread-safe; can be called at any time.
// SetSentinelAuthSecret configures the shared HMAC secret that the
// sentinel uses to sign its calls to /authorized-keys and /certs.
// Pass a slice that satisfies auth.SentinelMinSecretLen; shorter
// values cause those endpoints to fail closed (401 for every call).
// The expected source is CONTAINARIUM_SENTINEL_AUTH_SECRET on both
// the daemon and the sentinel.
func (gs *GatewayServer) SetSentinelAuthSecret(secret []byte) {
	gs.sentinelAuthSecret = secret
}

// SetSentinelPublicKey configures the sentinel's ed25519 public key (#688).
// When set, the sentinel endpoints additionally accept ed25519-signed requests
// (dual-accept with any configured HMAC secret). Source is
// CONTAINARIUM_SENTINEL_PUBLIC_KEY; the key is safe to distribute to any host,
