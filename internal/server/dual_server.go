//go:build !containarium_client

package server

import (
	"context"
	"fmt"
	"io"
	"k8s.io/client-go/kubernetes"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/alert"
	"go.opentelemetry.io/otel"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/autosleep"
	"github.com/footprintai/containarium/internal/cloud"
	clusterstore "github.com/footprintai/containarium/internal/cluster"
	"github.com/footprintai/containarium/internal/codeegress"
	"github.com/footprintai/containarium/internal/collaborator"
	appconfig "github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/coreguard"
	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/gateway"
	"github.com/footprintai/containarium/internal/guacamole"
	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	guardrailpolicypg "github.com/footprintai/containarium/internal/guardrailpolicy/pgstore"
	"github.com/footprintai/containarium/internal/guardrailrate"
	"github.com/footprintai/containarium/internal/metrics"
	"github.com/footprintai/containarium/internal/metrics/platformstats"
	"github.com/footprintai/containarium/internal/modelgateway"
	"github.com/footprintai/containarium/internal/mtls"
	"github.com/footprintai/containarium/internal/nicguard"
	"github.com/footprintai/containarium/internal/notify"
	"github.com/footprintai/containarium/internal/pentest"
	"github.com/footprintai/containarium/internal/policyreadiness"
	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/internal/retention"
	retentionpg "github.com/footprintai/containarium/internal/retention/pgstore"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/sandbox/ratelimit"
	secretsstore "github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/internal/security"
	"github.com/footprintai/containarium/internal/sshauthz"
	"github.com/footprintai/containarium/internal/tenantguard"
	"github.com/footprintai/containarium/internal/threatdetect"
	trackerstore "github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/traffic"
	"github.com/footprintai/containarium/internal/ttlsweeper"
	"github.com/footprintai/containarium/internal/waf"
	"github.com/footprintai/containarium/internal/wake"
	zapscanner "github.com/footprintai/containarium/internal/zap"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/catalogsig"
	clustercore "github.com/footprintai/containarium/pkg/core/cluster"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/crews"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	corecryptosecrets "github.com/footprintai/containarium/pkg/core/secrets"
	"github.com/footprintai/containarium/pkg/core/skills"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// DualServerConfig holds configuration for the dual server
// AnonDoorOptions are the daemon flags behind the anonymous-box
// guardrails (#2200). Rates are creates per 10 minutes, the unit the
// decision on #2204 was taken in.
type AnonDoorOptions struct {
	MaxBoxes        int
	KeyCreatesPer10 int
	KeyBurst        int
	IPCreatesPer10  int
	IPBurst         int
	StatePath       string
}

type DualServerConfig struct {
	// gRPC settings
	GRPCAddress string
	GRPCPort    int
	EnableMTLS  bool
	CertsDir    string

	// HTTP/REST settings
	HTTPPort   int
	EnableREST bool
	// StartupListener is HTTPPort, already bound by the daemon before its
	// startup waits and answering /health "degraded" (#3130). The gateway
	// takes it over; nil = the gateway binds HTTPPort itself.
	StartupListener *gateway.StartupListener

	// Authentication settings
	JWTSecret string

	// GuardrailStagingRoot is the daemon-side directory gated recipe deploys
	// read staged datasets from (#2368). Empty = gated deploys are refused.
	GuardrailStagingRoot string
	// GuardrailSnapshotDir is where the gate makes verification snapshots
	// ("" = the OS temp dir). Daemon-private, outside the staging root.
	GuardrailSnapshotDir string
	// GuardrailRateLimits are the per-subject limits on gated deploys and
	// guardrail policy Set/Get (#2404). Zero = disabled.
	GuardrailRateLimits GuardrailRateLimits

	// Swagger settings
	SwaggerDir string

	// App hosting settings (optional)
	EnableAppHosting   bool
	PostgresConnString string
	BaseDomain         string

	// AnonClaimURLBase is prefixed to anonymous-box claim tokens as
	// "<base>?token=…" in the guest's claim-url file (#2199), e.g.
	// https://<cloud-domain>/claim. Empty = the bare token is written.
	AnonClaimURLBase string
	// AnonReminderWebhook receives one POST per opt-in expiry reminder
	// (#2206) — the control plane emails the user; empty = reminders off.
	AnonReminderWebhook string
	// AnonDoor tunes the anonymous-box guardrails (#2200); zero values
	// mean anonbox.DefaultLimits / anonbox.DefaultDoorStatePath.
	AnonDoor      AnonDoorOptions
	CaddyAdminURL string

	// Route sync settings
	RouteSyncInterval time.Duration // Interval for syncing routes to Caddy (default 5s)
	// RunJournalRetention is how long a run's journal stays on a member box
	// before the reaper removes it (#2096). Zero = DefaultRunJournalRetention.
	RunJournalRetention time.Duration
	// RunCheckoutMaxAge is how old a platform run's per-run checkout under a
	// box user's ~/work must be before the daemon's sweep removes it (#3296).
	// Zero turns the sweep off (the daemon flag defaults to
	// DefaultRunCheckoutMaxAge).
	RunCheckoutMaxAge time.Duration
	// SSHAuthzMaxKeyAge is the self-host SSH authorization decider's maximum
	// key age (#2517): a key registered at least this long ago is refused
	// with KEY_EXPIRED. Zero = no limit.
	SSHAuthzMaxKeyAge time.Duration
	// SSHAuthzKeysCommand lets the jump host's AuthorizedKeysCommand call
	// SSHAuthzService.Authorize with a JWT carrying ssh:authorize, alongside
	// the sentinel signature (#2527). Off = the route stays sentinel-only.
	SSHAuthzKeysCommand bool

	// Caddy certificate directory for /certs endpoint (sentinel cert sync)
	CaddyCertDir string

	// VictoriaMetrics URL (auto-detected or provided)
	VictoriaMetricsURL string

	// OTelDropLabels are operator-supplied attribute keys that the
	// app-side OTel collector drops in addition to the built-in
	// PII/cardinality defaults (see DefaultOTelDropLabels). Empty
	// means "defaults only."
	OTelDropLabels []string

	// Host IP (extracted from network CIDR, e.g., "10.100.0.1")
	HostIP string

	// DaemonConfigStore for persisting daemon config to PostgreSQL (optional)
	DaemonConfigStore *app.DaemonConfigStore

	// Standalone mode: skip all PostgreSQL/core container dependencies
	Standalone bool

	// DisableSecurityScanner/DisablePentestScanner/DisableZapScanner turn
	// off the three passive/active scanners that otherwise start
	// automatically whenever PostgresConnString is set (there's no
	// independent opt-out today — each is gated purely on Postgres being
	// configured). All three run background work triggered by container
	// creation (the security scanner subscribes to CONTAINER_CREATED
	// events; ClamAV + a network pentest scan then run against every new
	// tenant), which is real, deliberate product behavior for a
	// production daemon but adds real per-create latency that a
	// throughput-sensitive deployment (or a benchmark) may want to skip.
	// Independent flags rather than one umbrella flag since an operator
	// may want e.g. malware scanning without the network pentest scan.
	DisableSecurityScanner bool
	DisablePentestScanner  bool
	DisableZapScanner      bool

	// Multi-backend peer settings
	SentinelURL    string   // URL for auto-discovering tunnel peers (e.g., "http://10.128.0.5:8081")
	Peers          []string // Static peer daemon addresses (e.g., ["10.0.0.12:8080"]); tunnel backends come from SentinelURL
	LocalBackendID string   // This daemon's backend ID (defaults to hostname)
	Pool           string   // Pool name to filter sentinel peer discovery (empty = no filter)
	Region         string   // Region this backend serves; recorded in the capability profile (#681). Falls back to Pool when empty.
	// PoolHeadroomFloors is each storage pool's floor in percent used, past
	// which creates and snapshots are refused (#3128). Unlisted pools use
	// poolcapacity.DefaultFloorPercent.
	PoolHeadroomFloors poolcapacity.Floors
	// CPUOvercommitFactor is the max CPU overcommit ceiling (committed cores /
	// physical cores) for create-time admission (#1029). <= 0 disables the gate.
	CPUOvercommitFactor float64
	// CPUOvercommitEnforce actually rejects over-ceiling creates when the gate
	// is enabled; false keeps it advisory (log-only).
	CPUOvercommitEnforce bool
	// PlacementCPUAware ranks pool placement by peer CPU commitment (least
	// committed wins) instead of first-healthy (#1029 direction 2).
	PlacementCPUAware bool

	// Sentinel primary registration (multi-pool routing). Empty PublicHostname
	// disables registration; the daemon still works as a single-pool primary.
	PublicHostname    string   // primary's own subdomain (e.g. prod.example.com)
	PublicAliases     []string // additional hostnames the primary's Caddy serves (e.g. api.example.com, voice.example.com)
	PublicBaseDomains []string // suffix-match anchors advertised to the sentinel; <anything>.<one-of-these> routes here. List multiple to host workloads under different parent domains on the same backend (see docs/PER-POOL-BASE-DOMAIN.md)
	PublicPort        int      // TLS port the sentinel forwards to (typically 443)

	// SSHHost is the public host clients dial to SSH into this daemon's
	// containers — the sentinel's SSH endpoint (e.g. region-a.example.com),
	// from --ssh-host. Surfaced on each Container.ssh_host so clients build
	// the connect target username@ssh_host without inferring it. Empty =
	// direct mode: ssh_host is left empty and clients use the container IP.
	SSHHost string

	// Alerting settings
	AlertWebhookURL    string // Webhook URL for alert notifications (optional)
	AlertWebhookSecret string // HMAC-SHA256 signing secret for webhook payloads (optional)

	// PROXY protocol: when true, configure Caddy with [proxy_protocol, tls]
	// listener_wrappers and trusted_proxies so containers receive the real
	// client IP via X-Forwarded-For. ProxyProtocolTrusted lists the CIDRs
	// allowed to send PROXY headers (typically the sentinel's VPC IP/32).
	ProxyProtocol        bool
	ProxyProtocolTrusted []string

	// CDN-fronted real client IP (#1829). ClientIPHeaders names the header(s)
	// Caddy derives the original client IP from (e.g. Cf-Connecting-Ip);
	// TrustedProxyCIDRs are the extra ranges (the CDN's published networks)
	// unioned into Caddy's trusted_proxies so that header is honored only from
	// them. Both empty = feature off. Independent of ProxyProtocol.
	ClientIPHeaders   []string
	TrustedProxyCIDRs []string

	// Runtime selects the box-lifecycle backend: "lxc" (default) or "k8s".
	// Set via CONTAINARIUM_RUNTIME env or --runtime flag on daemon start.
	Runtime string

	// ZFSTenantRoot is the ZFS dataset each tenant's encryptionroot is
	// created under (--zfs-tenant-root). Empty derives it from the daemon's
	// storage pool; see DefaultTenantRoot.
	ZFSTenantRoot string

	// ZFSKeysDir is the directory holding per-tenant encryption keys
	// (--zfs-keys-dir). Empty means no key custody is configured, which is
	// the default and leaves every encrypted create refused.
	ZFSKeysDir string
}

// managementRouteDomains returns the domains the daemon serves its own
// management UI/API apex on. These are the PublicBaseDomains — the same
// list advertised to the sentinel for suffix routing — so what Caddy
// serves and what the sentinel routes here stay identical by construction
// (#213). Falls back to the single BaseDomain when PublicBaseDomains is
// unset (e.g. configs built without resolvePublicBaseDomains), keeping
// single-domain deployments unchanged.
func managementRouteDomains(cfg *DualServerConfig) []string {
	if cfg == nil {
		return nil
	}
	if len(cfg.PublicBaseDomains) > 0 {
		return cfg.PublicBaseDomains
	}
	if cfg.BaseDomain != "" {
		return []string{cfg.BaseDomain}
	}
	return nil
}

// DualServer runs both gRPC and HTTP/REST servers
type DualServer struct {
	modelGateway             *modelgateway.Gateway // nil when the gateway is disabled
	config                   *DualServerConfig
	grpcServer               *grpc.Server
	internalLis              *auth.InternalListener // in-process transport for the REST gateway
	containerServer          *ContainerServer
	agentSkillServer         *AgentSkillServer        // run journal reaper (#2096)
	retentionReporter        *retention.Reporter      // dry-run retention sweep (#2876)
	notifyServer             *NotifyServer            // pushes waiting permission requests (#2185); nil without Postgres
	retentionChainSource     *retentionpg.ChainSource // audit_logs prefix purge (#2878); nil without an audit store
	credentialWatcher        *alert.CredentialWatcher // credential-expiry alert (#2371); nil without Postgres
	postgresRecovery         *postgresRecoveryWatcher // opt-in self-restart once Postgres is back (#2824); nil otherwise
	appServer                *AppServer
	networkServer            *NetworkServer
	trafficServer            *TrafficServer
	trafficCollector         *traffic.Collector
	gatewayServer            *gateway.GatewayServer
	tokenManager             *auth.TokenManager
	authMiddleware           *auth.AuthMiddleware
	routeStore               *app.RouteStore
	routeSyncJob             *app.RouteSyncJob
	passthroughStore         network.PassthroughStore
	passthroughSyncJob       *network.PassthroughSyncJob
	collaboratorStore        *collaborator.Store
	daemonConfigStore        *app.DaemonConfigStore
	metricsCollector         *metrics.Collector
	securityScanner          *security.Scanner
	securityStore            *security.Store
	securityServer           *SecurityServer
	auditStore               *audit.Store
	auditEventSubscriber     *audit.EventSubscriber
	auditAnchorManager       *audit.AnchorManager // periodic hash-chain-root anchoring (#1706)
	sshCollector             *audit.SSHCollector
	revocationStore          *auth.PgRevocationStore // Phase 1.2 — kill-switch for issued JWTs
	alertStore               *alert.Store
	alertManager             *alert.Manager
	alertDeliveryStore       *alert.DeliveryStore
	pentestManager           *pentest.Manager
	pentestStore             *pentest.Store
	zapManager               *zapscanner.Manager
	zapStore                 *zapscanner.Store
	peerPool                 *PeerPool
	autoSleepManager         *autosleep.Manager
	ttlSweeperManager        *ttlsweeper.Manager     // ephemeral CI box auto-delete (#299)
	anonManager              *anonbox.Manager        // anonymous-box door (#2197); nil unless CONTAINARIUM_ANON_DOOR=enable
	sandboxServer            *SandboxServer          // set in NewDualServer when incus.New succeeds; nil otherwise (#1488)
	sandboxTTLSweeperManager *ttlsweeper.Manager     // ephemeral sandbox auto-delete (#1488 Phase 4)
	secretsReconciler        *secretsReconciler      // Phase 4.3 Phase B-3
	networkPolicyEnforcer    *NetworkPolicyEnforcer  // #315 Phase A — eBPF per-tenant net policy (off unless configured)
	coreGuard                *coreguard.Reconciler   // #2084 — Incus NIC ACLs keeping tenants off core-role containers (on unless CONTAINARIUM_CORE_GUARD=off)
	tenantGuard              *tenantguard.Reconciler // #2347 — Incus NIC ACLs keeping tenants off each other (on unless CONTAINARIUM_TENANT_GUARD=off)

	// k8sNetPolicyReconciler converges tenant NetworkPolicy objects on the K8s
	// backend from the same store the eBPF enforcer reads (#1188). Nil on
	// every other runtime — the two are mutually exclusive by backend, not by
	// flag.
	k8sNetPolicyReconciler *K8sNetworkPolicyReconciler
	cloudClient            *cloud.Client // #354 — cloud-actuation client (nil unless host is enrolled)
	startTime              time.Time

	// threatDetectEngine is nil unless CONTAINARIUM_THREAT_SENTRY=1 AND the
	// eBPF network-policy object is loaded (#1640). threatDetectServer
	// always exists once networkPolicyEnforcer's block runs — it serves
	// GetSentryStatus regardless, reporting DISABLED/UNAVAILABLE explicitly
	// when the engine itself wasn't constructed.
	threatDetectEngine    *threatdetect.Engine
	processResponder      *ProcessResponder               // process guardrail responder (#2636); nil when the sentry is off/unavailable
	execHasher            *execHasher                     // miner-signature sha256 worker (#2638); nil without an engine
	sshdPosture           *security.SSHDPostureReconciler // box sshd key-only reconciler (#2424); nil when disabled
	threatDetectServer    *ThreatDetectionServer
	threatDetectNotifier  *threatdetect.WebhookNotifier // nil unless a finding store was built (armed + audit store) (#1643)
	threatDetectSweepStop context.CancelFunc

	// sentryHealthStore is the finding store the sentry-health loop (#2634)
	// writes SENTRY_UNAVAILABLE into. Built whenever the sentry is armed and
	// the audit store is up — independent of the eBPF object, because a
	// missing object is exactly the fault the loop has to record. nil when
	// unarmed (the loop never runs) or when no store could be built.
	sentryHealthStore sentryHealthStore
	threatSentryArmed bool               // CONTAINARIUM_THREAT_SENTRY=1
	sentryHealthStop  context.CancelFunc // stops the sentry-health loop and the webhook notifier
}

// containerEventKick returns a channel that receives a coalesced signal after
// each burst of container topology events (created, deleted, started, stopped,
// state-changed) until ctx is done, for reconcilers that wake early on
// container changes. Other bus events (metrics, traffic, findings, ...) do not
// kick, and a burst is debounced (#3050), so passes cannot run back to back.
func containerEventKick(ctx context.Context) <-chan struct{} {
	return containerTopologyKick(ctx, events.GetBus(), defaultContainerEventDebounce)
}

// NewDualServer creates a new dual server instance
func NewDualServer(config *DualServerConfig) (*DualServer, error) {
	// Audit C-MED-1: when --proxy-protocol is on, the daemon
	// trusts PROXY v2 headers from the configured CIDR list. An
	// empty or wildcard list means anyone on the network can
	// spoof the X-Forwarded-For chain. The L4 proxy layer
	// already refuses these inputs (l4_proxy.go:65-72) but only
	// at the point Caddy is reconfigured — by then the daemon
	// has been live for a while. Validate at startup so the
	// failure is visible at boot.
	if config.ProxyProtocol {
		if err := validateProxyProtocolTrusted(config.ProxyProtocolTrusted); err != nil {
			return nil, fmt.Errorf("proxy-protocol-trusted misconfigured: %w", err)
		}
	}
	// #1829: fail visibly at boot on a bad CDN client-IP trust config too.
	if err := validateClientIPHeaders(config.ClientIPHeaders); err != nil {
		return nil, fmt.Errorf("client-ip-header misconfigured: %w", err)
	}
	if err := validateTrustedProxyCIDRs(config.TrustedProxyCIDRs); err != nil {
		return nil, fmt.Errorf("trusted-proxy-cidrs misconfigured: %w", err)
	}
	// #2299: an unrecognised privileged-podman policy must not silently
	// become the permissive default, so refuse to start on one.
	if err := validatePrivilegedPolicyEnv(); err != nil {
		return nil, fmt.Errorf("privileged-podman policy misconfigured: %w", err)
	}

	// Create container server
	containerServer, err := NewContainerServer(config.Runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to create container server: %w", err)
	}

	// Wire the real cloud-metrics-export sinks (#1069). Without this,
	// SetMetricsExport always falls through the nil-sink path and
	// returns Unimplemented even with valid GCP credentials.
	containerServer.SetMetricsExportSinks(defaultMetricsExportSinks())
	// Wire the persisted daemon-config store BEFORE resuming export —
	// resume hydrates the enabled/provider flags from it. The store
	// used to reach the ContainerServer only via SetAlertManager, far
	// later in startup, so this hydration always read the disabled
	// default and resume-on-restart silently never fired (caught live
	// on a GCP backend while validating #1070).
	containerServer.SetDaemonConfigStore(config.DaemonConfigStore)
	// Box approval keys (#2184) are derived from the JWT secret, so answers
	// signed for a box's permission requests need no new stored secret.
	containerServer.SetApprovalKeySecret([]byte(config.JWTSecret))

	// Per-tenant encrypted storage (#1341). Wired here so the create path can
	// place an encrypted container on its tenant's pool; INERT until a
	// KeyProvider is also configured (#1342), because encryptionHooks.enabled()
	// is false without one. Nothing an operator can observe changes today.
	//
	// Failing to reach Incus (the k8s runtime, or a daemon with no local
	// Incus) leaves encryption unwired, which makes an encrypted create fail
	// loudly rather than land somewhere arbitrary.
	// Key custody (#1342). This is the switch that makes encrypted=true
	// reachable at all: without a provider, validateEncryption refuses every
	// encrypted create, which is the state every OSS daemon ships in.
	//
	// Installed BEFORE the storage wiring below only for readability —
	// SetKeyProvider re-attaches to hooks built either side of it, so the
	// order genuinely does not matter (asserted in key_provider_wiring_test).
	keyProvider, err := keyProviderFromDir(config.ZFSKeysDir)
	if err != nil {
		return nil, err
	}
	if keyProvider != nil {
		containerServer.SetKeyProvider(keyProvider)
		log.Printf("[encryption] key custody: file-based, keys under %s (--zfs-keys-dir). "+
			"Encrypted creates are now accepted on this daemon", config.ZFSKeysDir)
	}

	if encClient, err := incus.New(); err == nil {
		tenantRoot := config.ZFSTenantRoot
		derived := false
		if tenantRoot == "" {
			tenantRoot, derived = DefaultTenantRoot(encClient), true
		}
		if tenantRoot == "" {
			log.Printf("[encryption] per-tenant dataset root could not be derived from storage pool %q; "+
				"encrypted creates will be refused until --zfs-tenant-root is set", encClient.StoragePool())
		} else {
			containerServer.SetEncryptionStorage(tenantRoot, encClient)
			how := "--zfs-tenant-root"
			if derived {
				how = "derived from the storage pool"
			}
			log.Printf("[encryption] per-tenant dataset root: %s (%s)", tenantRoot, how)
		}

		// Container snapshots (#1160). Deliberately OUTSIDE the tenant-root
		// branch above: an unencrypted container has a dataset too, and that
		// is almost every container today — gating snapshots on encryption
		// being configured would ship the feature to nobody.
		//
		// It must come AFTER SetEncryptionStorage, though, because it reads
		// the encryption record to find which pool an encrypted container was
		// placed on. Wired first, every encrypted container would resolve to
		// the default pool's dataset — which is a different tenant's storage.
		containerServer.SetSnapshotStorage(zfscrypt.NewManager(nil), encClient.ContainerDataset)
	}
	// NOTE: metrics-export resume (StartMetricsExportIfEnabled) is
	// deliberately NOT called here. The resumed collector snapshots the
	// daemon's backend_id/region at build time, and neither is populated
	// this early in NewDualServer — localBackendID() returns "local" and
	// region is "" until SetPeerPool / SetCapabilityIdentity run in
	// Start(). Resuming here re-emitted every host's series under a second
	// ("local"/"") identity after each restart, splitting dashboards and
	// alerts keyed on backend_id (#1080). Resume is sequenced in Start()
	// after identity is wired instead.

	// Create token manager. Refuses to start if the JWT secret is
	// shorter than auth.MinSecretKeyLen — fail-closed on weak crypto
	// (audit finding A-MED-2).
	tokenManager, err := auth.NewTokenManager(config.JWTSecret, "containarium")
	if err != nil {
		return nil, fmt.Errorf("token manager: %w", err)
	}

	// #1679 — opt-in strict scopes: armed, RequireScope rejects a token
	// with no scopes claim instead of treating it as unrestricted. Off by
	// default (auth.SetStrictScopes(false) is also the zero value, but set
	// explicitly so re-running New on an already-armed process — tests —
	// doesn't leave a stale true from a previous daemon instance).
	auth.SetStrictScopes(appconfig.LoadAuth().StrictScopes)

	// Create auth middleware
	authMiddleware := auth.NewAuthMiddleware(tokenManager)

	// #1605 — the audit interceptor is created here, before the audit store
	// exists (Postgres connects later in this same function), because
	// grpc.NewServer()'s interceptor chain is fixed at construction. It's
	// armed in place via SetAuditGRPCInterceptor.SetStore below once the
	// store is ready; every call before that point is a no-op, same as the
	// gap this closes. Listed innermost (last) in both chains, directly
	// around the handler, so it captures the real RPC's status and
	// duration — the same position audit.HTTPAuditMiddleware occupies
	// relative to the HTTP auth middleware.
	auditGRPCInterceptor := audit.NewGRPCInterceptor()

	// Identity is bound to the transport, never to client-sent metadata:
	// the REST gateway reaches this server over an in-process listener (which
	// is trusted to forward JWT-verified claims), and external clients are
	// accepted only over mTLS, where identity comes from the verified client
	// certificate. Without --mtls there is no external gRPC listener at all.
	var tlsCreds credentials.TransportCredentials
	if config.EnableMTLS {
		certPaths := mtls.CertPathsFromDir(config.CertsDir)
		if !mtls.CertsExist(certPaths) {
			return nil, fmt.Errorf("TLS certificates not found in %s", config.CertsDir)
		}

		creds, err := mtls.LoadServerCredentials(certPaths)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		tlsCreds = creds
		log.Printf("gRPC server: external listener requires mTLS; identity comes from the client certificate")
	} else {
		log.Printf("gRPC server: external listener DISABLED (enable with --mtls); REST gateway uses an in-process listener")
	}
	internalLis := auth.NewInternalListener()

	grpcServer := grpc.NewServer(
		grpc.Creds(auth.NewServerTransportCredentials(tlsCreds)),
		// Transport-identity interceptor runs OUTER (first): a call it
		// rejects never reaches the platform-stats interceptor, so
		// unauthenticated noise never pollutes the platform.api.* series
		// (#1082) — those series reflect application-level API health.
		grpc.ChainUnaryInterceptor(
			auth.TransportIdentityUnaryInterceptor(),
			platformstats.UnaryInterceptor(containerServer.platformStats),
			auditGRPCInterceptor.Unary(),
		),
		grpc.ChainStreamInterceptor(
			auth.TransportIdentityStreamInterceptor(),
			auditGRPCInterceptor.Stream(),
		),
	)

	// Register container service
	pb.RegisterContainerServiceServer(grpcServer, containerServer)

	// SSHAuthzService (#2516): the per-connection SSH authorization
	// contract. Answers Unimplemented until a decider is wired below (the
	// self-host decider, #2517); the REST route is mounted behind the
	// sentinel signature.
	sshAuthzServer := NewSSHAuthzServer()
	pb.RegisterSSHAuthzServiceServer(grpcServer, sshAuthzServer)

	// ComposeAutostartService — operator-side RPC that execs
	// `agent-box compose <verb>` inside the tenant's LXC. Uses a
	// dedicated incus client (cheap; the gRPC server keeps the
	// reference for the process lifetime). Skipped on incus init
	// failure — the service is opt-in to the deploy that has incus.
	if composeIncusClient, err := incus.New(); err == nil {
		pb.RegisterComposeAutostartServiceServer(grpcServer, NewComposeAutostartServer(composeIncusClient))
		log.Printf("ComposeAutostartService registered (POST /v1/tenants/{username}/compose/{discover,enable,disable,status})")
	} else {
		log.Printf("Warning: ComposeAutostartService disabled — incus client init failed: %v", err)
	}

	// Create NetworkServer (always available for network topology)
	var networkServer *NetworkServer
	networkCIDR := "10.100.0.0/24" // Default, will be updated from incus
	networkIncusClient, err := incus.New()
	if err != nil {
		log.Printf("Warning: Failed to create incus client for network service: %v", err)
	} else {
		// Get actual network CIDR from incus
		if subnet, err := networkIncusClient.GetNetworkSubnet("incusbr0"); err == nil {
			if len(subnet) > 0 {
				networkCIDR = subnet
			}
		}
		// NetworkServer without app hosting dependencies (no proxy manager or app store)
		networkServer = NewNetworkServer(
			networkIncusClient,
			nil, // proxyManager - will be updated if app hosting is enabled
			nil, // appStore - will be updated if app hosting is enabled
			networkCIDR,
			"", // Proxy IP determined dynamically
		)
		pb.RegisterNetworkServiceServer(grpcServer, networkServer)
		log.Printf("Network service enabled")
	}

	// Register RecipeService — one-command deployment of declarative
	// GPU/app recipes. Pure orchestration over the container + network
	// servers; networkServer may be nil (expose then degrades to a warning).
	recipeServer := NewRecipeServer(containerServer, networkServer)
	pb.RegisterRecipeServiceServer(grpcServer, recipeServer)
	log.Printf("Recipe service enabled")

	// NetworkPolicyService — Phase A control-plane CRUD for per-tenant network
	// isolation policies (#315). Registered here with an in-memory store;
	// persistence (swap to PostgresNetworkPolicyStore once the pool exists) and
	// the per-veth TC_INGRESS BPF loader that consumes these policies are
	// follow-up increments. Admin-gated in the handler. Created before the
	// agent-skill service so it can compile a skill's allowed_peers into a
	// per-box network policy at launch (Phase 2 / #573).
	npServer := NewNetworkPolicyServer(NewMemNetworkPolicyStore())
	npServer.SetSignatureStore(NewMemNetworkPolicySignatureStore()) // #661 PR-B; swapped to Postgres below when available
	pb.RegisterNetworkPolicyServiceServer(grpcServer, npServer)
	log.Printf("NetworkPolicy service enabled (in-memory store; Phase A)")

	// CodingToolEgressPolicyService (#2378): the coding tool's egress
	// allowlist. In-memory until the Postgres pool exists; store only, no
	// enforcement yet.
	codeEgressServer := NewCodingToolEgressPolicyServer(NewMemCodingToolEgressPolicyStore())
	pb.RegisterCodingToolEgressPolicyServiceServer(grpcServer, codeEgressServer)

	// NotifyService (#2185) is registered only once its Postgres store is
	// up (below); nil leaves the RPCs unimplemented and the watcher off.
	var notifyServer *NotifyServer

	// GuardrailPolicyService (#2368): the admin-owned, cluster-wide guardrail
	// policy. It starts fail-closed; the real store is chosen by
	// guardrailPolicyStartupStore once postgresConnString is final (below).
	guardrailPolicyServer := NewGuardrailPolicyServer(unavailableGuardrailPolicyStore{})
	pb.RegisterGuardrailPolicyServiceServer(grpcServer, guardrailPolicyServer)
	// RetentionService (#2875): per-class retention windows and per-org legal
	// holds. The store is chosen by retentionStartupStore below.
	retentionServer := NewRetentionServer(unavailableRetentionStore{})
	pb.RegisterRetentionServiceServer(grpcServer, retentionServer)

	// Policy-store readiness (#2483): grpc.health.v1, one status per policy
	// service, NOT_SERVING while its store is on the fail-closed stand-in.
	// The REST gateway serves it at policyreadiness.HTTPPath for `doctor`;
	// the cloud status probe reads it in-process.
	policyStoreHealth := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, policyStoreHealth)

	// One bounded Postgres check gates every database-backed store below
	// (#2824): if it fails, they are skipped rather than each retried.
	pgGate := newPostgresStartupGate(startupPostgresTimeoutFromEnv(), pingPostgres, policyStoreHealth)

	// Register AgentSkillService — agent-as-a-box (Phase 0) + A2A transport
	// (Phase 1). Reuses the recipe server for box provisioning, the token
	// manager for minting the skill's scoped in-box token, and the network
	// policy server to compile allowed_peers into a per-box egress policy at
	// launch (Phase 2).
	agentSkillServer := NewAgentSkillServer(recipeServer, tokenManager, npServer)
	pb.RegisterAgentSkillServiceServer(grpcServer, agentSkillServer)
	log.Printf("Agent-skill service enabled")

	// #1922 — one shared in-memory registry of currently-live runs, so
	// the tracker broker's ClaimTrackerIssue liveness check and identity
	// stamp can resolve run_id -> skill/model without a database round
	// trip. AgentSkillServer populates it (RunAgentSkill / endRunLease);
	// ContainerServer's tracker write RPCs read it.
	runRegistry := runlease.NewRegistry()
	agentSkillServer.SetRunRegistry(runRegistry)
	agentSkillServer.SetPlatformMCPPort(config.HTTPPort)
	agentSkillServer.SetRunJournalRetention(config.RunJournalRetention)
	containerServer.SetRunRegistry(runRegistry)
	containerServer.SetRunCheckoutMaxAge(config.RunCheckoutMaxAge)

	// #1922 — one shared ClaimLocks so concurrent ClaimTrackerIssue calls
	// for the same (username, connection, issue) serialize within this
	// daemon process.
	containerServer.SetClaimLocks(trackerstore.NewClaimLocks())

	// #1923 — no explicit wiring needed: SubmitTrackerChange's
	// boxRunnerForSubmit falls back to containerServer's own *manager
	// field, already set above, which satisfies submit.BoxRunner
	// (asserted at compile time in tracker_submit_server.go).

	// Register CrewService — Phase 3. Collaborating sets of skills bound to a
	// task purpose; reuses the agent-skill server to provision each member box.
	crewServer := NewCrewServer(agentSkillServer)
	pb.RegisterCrewServiceServer(grpcServer, crewServer)
	log.Printf("Crew service enabled")

	// Merge external skill/crew catalogs (#620) into the process-wide catalogs
	// the agent/crew servers use, so out-of-tree packs register without a
	// rebuild. Skills first (crews reference them). Best-effort: a bad external
	// catalog logs and is skipped rather than failing daemon startup.
	//
	// Optional provenance check (#648): when require-signed mode is on, each
	// external catalog file must carry a valid detached signature under a
	// trusted key. catalogVerifier is nil when the mode is off (load unsigned,
	// as before). A misconfigured require-signed mode (e.g. no trusted key)
	// fails closed — we skip loading external catalogs rather than fall back to
	// unsigned.
	catalogVerifier, verr := catalogsig.FromEnv()
	if verr != nil {
		log.Printf("[catalog] require-signed mode misconfigured, skipping external catalogs: %v", verr)
	} else {
		if dir := os.Getenv("CONTAINARIUM_SKILLS_DIR"); dir != "" {
			if err := skills.GetDefault().LoadDirVerified(dir, catalogVerifier); err != nil {
				log.Printf("[agent-skill] external skills from %s: %v", dir, err)
			} else {
				log.Printf("Loaded external skills from %s", dir)
			}
		}
		// #2488: compose the pinned engineering standard into hosted skills
		// that declare one. Runs after external catalogs so their skills can
		// declare a standard too. Opt-in; any failure keeps the inline prompt.
		if msg := applyPinnedStandard(skills.GetDefault(), os.Getenv(sdlcBundleEnv), loadSDLCBundle); msg != "" {
			log.Print(msg)
		}
		if dir := os.Getenv("CONTAINARIUM_CREWS_DIR"); dir != "" {
			if err := crews.GetDefault().LoadDirVerified(dir, catalogVerifier); err != nil {
				log.Printf("[crew] external crews from %s: %v", dir, err)
			} else {
				log.Printf("Loaded external crews from %s", dir)
			}
		}
	}

	// Register BackupService — logical (pg_dump) database backups for the
	// databases running inside containers, stored off-host (local dir or
	// GCS). Orchestration over the container manager; the GCS uploader is
	// best-effort (LOCAL-only if `gcloud` is absent). See
	// docs/DB-BACKUP-OPERATIONS.md.
	// A configured-but-unloadable managed-backup key wrapper (#2402) is
	// a startup error, not a warning: silently serving legacy backups
	// where managed ones were configured is the failure this refuses.
	backupServer, err := NewBackupServer(containerServer)
	if err != nil {
		return nil, fmt.Errorf("backup service: %w", err)
	}
	pb.RegisterBackupServiceServer(grpcServer, backupServer)
	// Metrics export's backup-health series (#2294) reads the same
	// backup core backupServer orchestrates — wired here since
	// BackupServer depends on ContainerServer, not the reverse.
	containerServer.SetBackupManager(backupServer.Manager())
	log.Printf("Backup service enabled")

	// Register VolumeService — CephFS on Incus and RWX PVCs on Kubernetes.
	pb.RegisterVolumeServiceServer(grpcServer, NewVolumeServer(config.Runtime))
	log.Printf("Volume service enabled")

	// Register SandboxService — ephemeral, no-SSH sandboxes (#1488). Own
	// incus client, same pattern as the other feature servers in this
	// function.
	//
	// Pool is nil: SandboxServer can use a configured *pool.Pool (#1520)
	// to serve spawns from a warm ring instead of always creating cold,
	// but nothing constructs one here yet. A pool needs operator-facing
	// config this daemon doesn't have a source for yet (min_warm per
	// template, an image per template, the IPAM range, the NIC network)
	// — and those need validating against a live host before they're
	// safe defaults, which this can't do. Every spawn takes the cold
	// path until that config lands; see SandboxServer's own type doc for
	// why nil is also the ONLY safe default (a configured-but-empty pool
	// would silently start rejecting every caller that hasn't set
	// allow_cold_start=true).
	var sandboxServer *SandboxServer
	if sandboxIncusClient, err := incus.New(); err == nil {
		sandboxServer = NewSandboxServer(sandboxIncusClient, nil)

		// Per-tenant spawn rate limit (#1488 Phase 4). Off by default
		// (ratelimit.Disabled(), same as NewSandboxServer's own zero
		// value) — CONTAINARIUM_SANDBOX_SPAWN_RATE_PER_MINUTE opts in.
		// A malformed value fails closed (every spawn refused, not
		// silently unlimited) rather than aborting daemon startup —
		// same posture as clusterCaps.configErr below, applied to a
		// feature that's off by default instead of one with a
		// meaningful "unset" ceiling.
		spawnLimiter := ratelimit.NewFromEnv(
			os.Getenv("CONTAINARIUM_SANDBOX_SPAWN_RATE_PER_MINUTE"),
			os.Getenv("CONTAINARIUM_SANDBOX_SPAWN_BURST"),
		)
		if limiterErr := spawnLimiter.Err(); limiterErr != nil {
			log.Printf("ERROR: %v — sandbox spawns will be refused until the rate limit config is fixed", limiterErr)
		}
		sandboxServer.SetRateLimiter(spawnLimiter)

		pb.RegisterSandboxServiceServer(grpcServer, sandboxServer)
		log.Printf("Sandbox service enabled")
	} else {
		log.Printf("Warning: SandboxService disabled — incus client init failed: %v", err)
	}

	// Register ClusterService — managed Kubernetes clusters (#1413).
	// Lifecycle state only; VM provisioning is the reconciler's job
	// (#1414). Starts on the in-memory store and is swapped to Postgres
	// below once the pool is up (same degrade posture as the network
	// policy and crew-run stores).
	clusterServer := NewClusterServer(clusterstore.NewMemStore())
	if capsErr := clusterServer.SetCapsFromEnv(
		os.Getenv("CONTAINARIUM_CLUSTER_MAX_NODES"),
		os.Getenv("CONTAINARIUM_CLUSTER_MAX_NODE_SIZE")); capsErr != nil {
		// Fail closed: a typo'd cap must not become an unlimited one.
		clusterServer.SetCaps(clusterCaps{configErr: capsErr})
		log.Printf("ERROR: %v — cluster creates/updates will be refused until the caps are fixed", capsErr)
	}
	// Node isolation opt-in (#1428): unset means container node pools
	// are refused on this host, which is the right default for a shared
	// multi-tenant one. A typo'd value refuses them too, loudly.
	clusterServer.SetIsolationGateFromEnv(os.Getenv("CONTAINARIUM_CLUSTER_ALLOW_CONTAINER_NODES"))
	pb.RegisterClusterServiceServer(grpcServer, clusterServer)
	log.Printf("Cluster service enabled (in-memory store; Postgres swap below)")

	// KMS admin service — read the active backend, envelope
	// coverage, and trigger legacy→envelope migration. Backed by
	// the same secrets Store; backend *config* stays in env/systemd.
	pb.RegisterKmsServiceServer(grpcServer, NewKmsServer(containerServer))

	// Cloud-actuation client (#354) is constructed later, once routeStore is
	// finalized — the container actuator needs it to expose cloud routes at the
	// host edge. Declared here so it's in scope for the DualServer assembly.
	var cloudClient *cloud.Client

	// Create TrafficServer (always available, but conntrack only works on Linux)
	var trafficServer *TrafficServer
	var trafficCollector *traffic.Collector
	if networkIncusClient != nil {
		// Traffic collector needs PostgreSQL - will be set up later if app hosting enabled
		// For now, create without store and update later
		emitter := events.NewEmitter(events.GetBus())
		collectorConfig := traffic.DefaultCollectorConfig()
		collectorConfig.NetworkCIDR = networkCIDR

		// Create collector without store initially
		trafficCollector, err = traffic.NewCollector(collectorConfig, networkIncusClient, nil, emitter)
		if err != nil {
			log.Printf("Warning: Failed to create traffic collector: %v", err)
		} else {
			trafficServer = NewTrafficServer(trafficCollector)
			pb.RegisterTrafficServiceServer(grpcServer, trafficServer)
			if trafficCollector.IsAvailable() {
				log.Printf("Traffic monitoring service enabled (conntrack available)")
			} else {
				log.Printf("Traffic monitoring service enabled (conntrack unavailable - Linux only)")
			}
		}
	}

	// Create and register AppServer if app hosting is enabled
	var appServer *AppServer
	var routeStore *app.RouteStore
	var routeSyncJob *app.RouteSyncJob
	// coreServices is hoisted so alert setup can reference it later
	var coreServices *CoreServices
	// postgresConnString is hoisted so collaborator init (after skipAppHosting) can use it
	postgresConnString := config.PostgresConnString
	if config.EnableAppHosting {
		incusClient, err := incus.New()
		if err != nil {
			log.Printf("Warning: Failed to create incus client for app hosting: %v. App hosting disabled.", err)
		} else {
			// Determine PostgreSQL connection string and Caddy admin URL
			caddyAdminURL := config.CaddyAdminURL
			networkCIDR := "10.100.0.0/24"

			// Get actual network CIDR from incus
			if subnet, err := incusClient.GetNetworkSubnet("incusbr0"); err == nil {
				// Convert "10.100.0.1/24" to "10.100.0.0/24"
				parts := subnet
				if len(parts) > 0 {
					networkCIDR = subnet
				}
			}

			// If no external PostgreSQL/Caddy provided, set up core services
			if postgresConnString == "" || caddyAdminURL == "" {
				log.Printf("Setting up core services (PostgreSQL, Caddy) in containers...")

				coreServices = NewCoreServices(incusClient, CoreServicesConfig{
					NetworkCIDR: networkCIDR,
				})

				// Setup PostgreSQL if not provided
				if postgresConnString == "" {
					connString, err := coreServices.EnsurePostgres(context.Background())
					if err != nil {
						log.Printf("Warning: Failed to setup PostgreSQL: %v. App hosting disabled.", err)
						goto skipAppHosting
					}
					postgresConnString = connString
					log.Printf("PostgreSQL ready: %s", coreServices.GetPostgresIP())
				}

				// Setup Caddy if not provided
				if caddyAdminURL == "" && config.BaseDomain != "" {
					adminURL, err := coreServices.EnsureCaddy(context.Background(), config.BaseDomain)
					if err != nil {
						log.Printf("Warning: Failed to setup Caddy: %v. Proxy features disabled.", err)
					} else {
						caddyAdminURL = adminURL
						caddyIP := coreServices.GetCaddyIP()
						log.Printf("Caddy ready: %s", caddyIP)

						// Now that Caddy exists, set up host:80/443 → caddy port
						// forwarding. The earlier auto-detect at daemon startup
						// (see cmd/daemon.go:199) ran BEFORE Caddy was spawned
						// on first install, so it skipped this step. Re-running
						// it here makes first-install work without requiring a
						// daemon restart.
						if network.CheckIPTablesAvailable() {
							pf := network.NewPortForwarderWithNetwork(caddyIP, networkCIDR)
							if err := pf.SetupPortForwarding(); err != nil {
								log.Printf("Warning: Failed to setup port forwarding after Caddy bring-up: %v", err)
								log.Printf("  External HTTPS for %s may not work", config.BaseDomain)
							}
						}

					}
				}
			}

			// Boxes reach this host's apps by public name through the kernel
			// hairpin rule (#2732), so the bridge DNS record earlier daemons
			// wrote is removed on upgrade (#2734). No base domain: untouched.
			if err := cleanupBridgeDNSRecord(incusClient, "incusbr0", config.BaseDomain, config.SSHHost); err != nil {
				log.Printf("Warning: could not remove the old bridge DNS record: %v", err)
			}

			// Setup VictoriaMetrics + Grafana if no URL provided
			victoriaMetricsURL := config.VictoriaMetricsURL
			if victoriaMetricsURL == "" {
				// Determine PostgreSQL IP for Grafana config DB
				var postgresIP string
				if coreServices != nil {
					postgresIP = coreServices.GetPostgresIP()
				}
				// If coreServices wasn't created (core containers were pre-detected),
				// look up the PostgreSQL IP from the existing container
				if postgresIP == "" {
					if pgInfo, err := incusClient.FindContainerByRole(incus.RolePostgres); err == nil {
						postgresIP = pgInfo.IPAddress
					}
				}
				if postgresIP != "" {
					// Ensure coreServices exists for EnsureVictoriaMetrics
					if coreServices == nil {
						coreServices = NewCoreServices(incusClient, CoreServicesConfig{
							NetworkCIDR: networkCIDR,
						})
					}
					vmIP, err := coreServices.EnsureVictoriaMetrics(context.Background(), postgresIP)
					if err != nil {
						log.Printf("Warning: Failed to setup VictoriaMetrics: %v. Monitoring disabled.", err)
					} else {
						victoriaMetricsURL = fmt.Sprintf("http://%s:%d", vmIP, DefaultVMPort)
						log.Printf("VictoriaMetrics ready: %s", vmIP)
					}
				}
			}
			config.VictoriaMetricsURL = victoriaMetricsURL

			// Setup alerting (vmalert + Alertmanager) if VictoriaMetrics is available
			if victoriaMetricsURL != "" {
				if coreServices == nil {
					coreServices = NewCoreServices(incusClient, CoreServicesConfig{
						NetworkCIDR: networkCIDR,
					})
				}
				// If a signing secret is configured, route Alertmanager through the
				// daemon's relay endpoint so payloads get HMAC-signed before forwarding.
				alertTargetURL := config.AlertWebhookURL
				if config.AlertWebhookSecret != "" && config.AlertWebhookURL != "" && config.HostIP != "" {
					alertTargetURL = fmt.Sprintf("http://%s:%d/internal/alert-relay", config.HostIP, config.HTTPPort)
				}
				if err := coreServices.SetupAlerting(context.Background(), alertTargetURL); err != nil {
					log.Printf("Warning: Failed to setup alerting: %v. Alerting disabled.", err)
				} else {
					log.Printf("Alerting ready (vmalert + Alertmanager)")
				}
			}

			// Setup the app-side OTel collector. Brings up
			// otelcol-contrib as a core LXC, points its OTLP
			// exporter at the local VictoriaMetrics, and wires the
			// resulting OTLP/HTTP endpoint into ContainerServer so
			// new monitoring=true containers get OTEL_*
			// env-stamping (see PR #175). Only runs when VM is
			// available — without a sink there's nothing for the
			// collector to forward to.
			// #2103: on a host whose metrics container was auto-detected,
			// EnsureVictoriaMetrics never runs, so the #2079 Grafana
			// hardening backfill has to happen here. Reuses coreServices
			// when it exists, builds a minimal one otherwise, and hands it
			// on so the OTel block below sees the same instance.
			if victoriaMetricsURL != "" {
				coreServices = hardenDetectedGrafana(coreServices, incusClient)
			}

			if victoriaMetricsURL != "" && coreServices != nil {
				vmIP := coreServices.GetVictoriaMetricsIP()
				if vmIP == "" {
					if pgInfo, err := incusClient.FindContainerByRole(incus.RoleVictoriaMetrics); err == nil {
						vmIP = pgInfo.IPAddress
					}
				}
				if vmIP != "" {
					if _, err := coreServices.EnsureOTelCollector(context.Background(), vmIP, config.OTelDropLabels); err != nil {
						log.Printf("Warning: Failed to setup OTel collector: %v. App-emitted telemetry disabled.", err)
					} else {
						endpoint := coreServices.GetOTelCollectorEndpoint()
						containerServer.SetOTelCollectorEndpoint(endpoint)
						containerServer.SetCoreServices(coreServices)
						log.Printf("OTel collector ready: %s (drop-labels=%v)", endpoint, config.OTelDropLabels)
					}
				}
			}

			// Set up the tenant secrets store. Mirrors the
			// app-store path: shares Postgres but holds its own
			// pgxpool. Failure here only disables the secrets
			// API; the rest of the daemon keeps running.
			if secretsPool, secretsErr := pgGate.connect(postgresConnString, 5, 3*time.Second); secretsErr != nil {
				log.Printf("Warning: Failed to connect to Postgres for secrets store: %v", secretsErr)
			} else {
				key, created, kerr := corecryptosecrets.LoadOrCreateMasterKey("/etc/containarium/secrets.key")
				if kerr != nil {
					log.Printf("Warning: Failed to load secrets master key: %v. Secrets disabled.", kerr)
					secretsPool.Close()
				} else {
					if created {
						log.Printf("[secrets] NEW MASTER KEY generated at /etc/containarium/secrets.key — back this up off-host, losing it means every stored secret is unrecoverable ciphertext")
					}
					cipher, cerr := corecryptosecrets.NewCipher(key)
					if cerr != nil {
						log.Printf("Warning: Failed to construct secrets cipher: %v. Secrets disabled.", cerr)
						secretsPool.Close()
					} else {
						// Phase 4.1 — KMS backend selection via env
						// CONTAINARIUM_KMS_BACKEND=none|inproc|vault.
						// On err the store still opens in legacy mode
						// (no envelope) — operators see the WARNING
						// rather than the daemon refusing to start.
						kms, kdesc, kerr := secretsstore.LoadKMSClient(key)
						if kerr != nil {
							log.Printf("Warning: KMS backend config error: %v. Secrets store falling back to legacy mode.", kerr)
							kms = nil
							kdesc = "disabled (config error)"
						}
						// #1630 — per-tenant KEK factory, only non-nil
						// when CONTAINARIUM_KMS_BACKEND=gcp. A config
						// error here degrades the SAME way the shared
						// client does: log and disable, don't fail the
						// whole secrets store over it.
						tenantKMSFactory, tkerr := secretsstore.LoadTenantKMSFactory()
						if tkerr != nil {
							log.Printf("Warning: per-tenant KMS factory config error: %v. SetTenantKMSKey will be unavailable.", tkerr)
							tenantKMSFactory = nil
						}
						// Phase 4.1 Phase-E — master-key retirement
						// gate. CONTAINARIUM_REQUIRE_ENVELOPE=true
						// means every decrypt must go through KMS;
						// legacy rows are rejected. Refuse to wire
						// the Store if the gate is on but no KMS
						// backend is configured — fail-closed at
						// startup is the only safe shape.
						requireEnvelope := envBool("CONTAINARIUM_REQUIRE_ENVELOPE")
						// Snapshot KMS status for the KmsService
						// GetKMSStatus RPC, regardless of whether the
						// store ends up wired (the status is about
						// config, not store health).
						containerServer.SetKMSStatus(os.Getenv("CONTAINARIUM_KMS_BACKEND"), kdesc, kms != nil, requireEnvelope)
						if requireEnvelope && kms == nil {
							log.Printf("FATAL: CONTAINARIUM_REQUIRE_ENVELOPE=true but no KMS backend is configured. Set CONTAINARIUM_KMS_BACKEND=vault (or inproc for dev) before enabling retirement mode. Secrets disabled.")
							secretsPool.Close()
						} else {
							var opts []secretsstore.Option
							if kms != nil {
								opts = append(opts, secretsstore.WithKMS(kms))
							}
							if requireEnvelope {
								opts = append(opts, secretsstore.WithRequireEnvelope(true))
							}
							if tenantKMSFactory != nil {
								opts = append(opts, secretsstore.WithTenantKMSFactory(tenantKMSFactory))
							}
							if store, serr := secretsstore.NewStore(context.Background(), secretsPool, cipher, opts...); serr != nil {
								log.Printf("Warning: Failed to init secrets store: %v. Secrets disabled.", serr)
								secretsPool.Close()
							} else {
								containerServer.SetSecretsStore(store)
								retirement := ""
								if requireEnvelope {
									retirement = " [legacy-rejected]"
								}
								log.Printf("Secrets store ready (file-keyed AES-256-GCM, envelope: %s%s)", kdesc, retirement)
							}
						}
					}
				}
			}

			// Set up the tracker-connections store (#1921 step 2) — where
			// a tenant's issue tracker is, and which broker-only secret
			// holds its credential. Mirrors the secrets-store path
			// directly above: shares Postgres but holds its own pgxpool.
			// Failure here only disables the TrackerService API; the rest
			// of the daemon keeps running.
			if trackerPool, trackerErr := pgGate.connect(postgresConnString, 5, 3*time.Second); trackerErr != nil {
				log.Printf("Warning: Failed to connect to Postgres for tracker store: %v", trackerErr)
			} else if trkStore, terr := trackerstore.NewStore(context.Background(), trackerPool); terr != nil {
				log.Printf("Warning: Failed to init tracker store: %v. Tracker connections disabled.", terr)
				trackerPool.Close()
			} else {
				containerServer.SetTrackerStore(trkStore)
				agentSkillServer.SetTrackerConnections(trkStore)
				// #2062: a run's leftover fan-out reservations are swept
				// when its lease ends.
				agentSkillServer.SetLineageReservations(trkStore)
				// #2022: dispatched runs start through the RunAgentSkill path.
				containerServer.SetTrackerRunStarter(NewTrackerRunStarter(agentSkillServer))
				pb.RegisterTrackerServiceServer(grpcServer, containerServer)
				log.Printf("Tracker connection store ready")
			}

			// Notification channels (#2185): where a waiting `code run
			// --approvals ask` request is pushed. Own pgxpool, like the
			// tracker store; failure only disables NotifyService.
			if notifyPool, notifyErr := pgGate.connect(postgresConnString, 3, 3*time.Second); notifyErr != nil {
				log.Printf("Warning: Failed to connect to Postgres for notify store: %v", notifyErr)
			} else if ntfStore, nerr := notify.NewPGStore(context.Background(), notifyPool); nerr != nil {
				log.Printf("Warning: Failed to init notify store: %v. Notifications disabled.", nerr)
				notifyPool.Close()
			} else {
				// A nil *secrets.Store must stay a nil interface: the
				// service then refuses tokens instead of dereferencing it.
				var notifySecrets notifySecretStore
				if containerServer.secretsStore != nil {
					notifySecrets = containerServer.secretsStore
				}
				// Private networks a self-hosted ntfy may be on; a bad value
				// fails closed (public servers only).
				allowed, aerr := notify.ParseAllowedCIDRs(os.Getenv(notify.AllowedCIDRsEnv))
				if aerr != nil {
					log.Printf("Warning: %v; notifications reach public ntfy servers only", aerr)
					allowed = nil
				}
				notifyServer = NewNotifyServer(ntfStore, notifySecrets, containerServer.boxRunExecFn, containerServer.boxRunDir(), allowed)
				pb.RegisterNotifyServiceServer(grpcServer, notifyServer)
				log.Printf("Notify channel store ready")
			}

			// Connect to app store
			appStore, err := gatedStore(pgGate, postgresConnString, app.NewStore)
			if err != nil {
				log.Printf("Warning: Failed to connect to app store: %v. App hosting disabled.", err)
			} else {
				appManager := app.NewManager(appStore, incusClient, app.ManagerConfig{
					BaseDomain:    config.BaseDomain,
					CaddyAdminURL: caddyAdminURL,
				})
				appServer = NewAppServer(appManager, appStore)
				pb.RegisterAppServiceServer(grpcServer, appServer)
				log.Printf("App hosting service enabled")

				// Update NetworkServer with app hosting dependencies
				if networkServer != nil {
					var proxyManager *app.ProxyManager

					if caddyAdminURL != "" {
						proxyManager = byocIngressFromEnv(wafIngressFromEnv(app.NewProxyManager(caddyAdminURL, config.BaseDomain).WithDNSChallenge(app.DNSChallengeFromEnv())))
						// Ensure Caddy has basic server config for routes
						if err := proxyManager.EnsureServerConfig(); err != nil {
							log.Printf("Warning: Failed to ensure Caddy server config: %v", err)
						}
						if config.ProxyProtocol {
							if err := proxyManager.EnableProxyProtocol(config.ProxyProtocolTrusted); err != nil {
								log.Printf("Warning: Failed to enable PROXY protocol on Caddy: %v", err)
							} else {
								log.Printf("Caddy listener_wrappers: PROXY v2 enabled, trusted=%v", config.ProxyProtocolTrusted)
							}
						}
						if len(config.ClientIPHeaders) > 0 || len(config.TrustedProxyCIDRs) > 0 {
							if err := proxyManager.ConfigureClientIP(config.ClientIPHeaders, config.TrustedProxyCIDRs); err != nil {
								log.Printf("Warning: Failed to configure client-IP trust on Caddy: %v", err)
							} else {
								log.Printf("Caddy client-IP trust: headers=%v extra_trusted=%v", config.ClientIPHeaders, config.TrustedProxyCIDRs)
							}
						}

						// With DNS-01 configured, provision a single `*.<base-domain>`
						// wildcard so every per-region / subdomain endpoint
						// (region-a.<base>, …) gets a cert from one issuance — no
						// per-hostname HTTP-01, which sidesteps both the HTTP-01
						// redirect failure and Let's Encrypt per-domain rate limits
						// as regions scale (#389). Best-effort; per-route ProvisionTLS
						// still covers individual hostnames if this is skipped.
						if proxyManager.HasDNSChallenge() {
							if err := proxyManager.ProvisionWildcardTLS(); err != nil {
								log.Printf("Warning: failed to provision wildcard TLS (*.%s): %v", config.BaseDomain, err)
							} else {
								log.Printf("Caddy TLS: provisioned wildcard *.%s via DNS-01", config.BaseDomain)
							}
						}

						// Create L4ProxyManager for TLS passthrough (SNI-based) routing
						l4ProxyManager := app.NewL4ProxyManager(caddyAdminURL)
						if config.ProxyProtocol {
							if err := l4ProxyManager.EnableL4ProxyProtocol(config.ProxyProtocolTrusted); err != nil {
								log.Printf("Warning: Failed to enable PROXY protocol on caddy-l4: %v", err)
							}
						}

						// Create RouteStore for persistent route storage
						routeStore, err = app.NewRouteStore(context.Background(), appStore.Pool())
						if err != nil {
							log.Printf("Warning: Failed to create route store: %v", err)
						} else {
							// Create RouteSyncJob to sync PostgreSQL -> Caddy
							syncInterval := config.RouteSyncInterval
							if syncInterval == 0 {
								syncInterval = 5 * time.Second
							}
							routeSyncJob = app.NewRouteSyncJob(routeStore, proxyManager, syncInterval)
							routeSyncJob.SetL4ProxyManager(l4ProxyManager)
							log.Printf("Route persistence enabled with %v sync interval", syncInterval)
						}
					}
					networkServer.proxyManager = proxyManager
					networkServer.appStore = appStore
					networkServer.routeStore = routeStore
					networkServer.baseDomain = config.BaseDomain
					log.Printf("Network service updated with app hosting features")

					// ContainerServer also needs route store + proxy
					// manager so DeleteContainer can cascade-clean.
					containerServer.SetRouteCleanupDeps(routeStore, proxyManager)
				}

				// Update TrafficCollector with store for persistence
				if trafficCollector != nil && postgresConnString != "" {
					trafficStore, err := gatedStore(pgGate, postgresConnString, traffic.NewStore)
					if err != nil {
						log.Printf("Warning: Failed to create traffic store: %v. Traffic persistence disabled.", err)
					} else {
						// Re-create collector with store
						emitter := events.NewEmitter(events.GetBus())
						collectorConfig := traffic.DefaultCollectorConfig()
						collectorConfig.NetworkCIDR = networkCIDR
						collectorConfig.PostgresConnString = postgresConnString

						newCollector, err := traffic.NewCollector(collectorConfig, incusClient, trafficStore, emitter)
						if err != nil {
							log.Printf("Warning: Failed to update traffic collector with store: %v", err)
						} else {
							trafficCollector = newCollector
							trafficServer = NewTrafficServer(trafficCollector)
							log.Printf("Traffic monitoring updated with persistence")
						}
					}
				}
			}
		}
	}
skipAppHosting:

	// For standalone peers (or any daemon without app hosting), ensure
	// core-postgres is provisioned so security scanning and other DB-dependent
	// features work. This follows the federated architecture where each node
	// is self-sufficient.
	if postgresConnString == "" && !config.EnableAppHosting {
		if envPG := os.Getenv("CONTAINARIUM_POSTGRES_URL"); envPG != "" {
			postgresConnString = envPG
		} else {
			// Try to auto-detect existing postgres container first
			if incusClient, err := incus.New(); err == nil {
				if pgInfo, err := incusClient.FindContainerByRole(incus.RolePostgres); err == nil && pgInfo.IPAddress != "" {
					// The same password the app-hosting path resolves (secret
					// file, env, then the dev default), not the compiled-in
					// default unconditionally (#2091).
					pgPassword, _, pwErr := ResolvePostgresPassword()
					if pwErr != nil {
						log.Printf("ERROR: %v — using the compiled-in default for the detected Postgres", pwErr)
						pgPassword = DefaultPostgresPassword
					}
					postgresConnString = PostgresDSN(DefaultPostgresUser, pgPassword,
						pgInfo.IPAddress, DefaultPostgresPort, DefaultPostgresDB)
					log.Printf("Detected existing PostgreSQL at: %s", pgInfo.IPAddress)
					// Re-apply the systemd Restart=on-failure override even
					// for pre-existing containers. The non-app-hosting code
					// path doesn't go through EnsurePostgres (which would
					// normally apply this), so containers provisioned before
					// the restart-policy code existed — or rebuilt from a
					// template that pre-dates it — will silently lack
					// auto-restart on OOM. Idempotent.
					cs := NewCoreServices(incusClient, CoreServicesConfig{})
					cs.ensurePostgresRestartPolicy()
				} else {
					// No postgres container found — provision one
					log.Printf("Provisioning core-postgres for local security scanning...")
					networkCIDR := "10.100.0.0/24"
					if subnet, err := incusClient.GetNetworkSubnet("incusbr0"); err == nil {
						networkCIDR = subnet
					}
					cs := NewCoreServices(incusClient, CoreServicesConfig{
						NetworkCIDR: networkCIDR,
					})
					connString, err := cs.EnsurePostgres(context.Background())
					if err != nil {
						log.Printf("Warning: Failed to provision core-postgres: %v. DB-dependent features disabled.", err)
					} else {
						postgresConnString = connString
						log.Printf("Core PostgreSQL ready: %s", cs.GetPostgresIP())
						// Keep coreServices reference for security container provisioning
						if coreServices == nil {
							coreServices = cs
						}
					}
				}
			}
		}
	}

	// Setup collaborator store and manager (independent of app hosting)
	// postgresConnString was set by app hosting setup above, or from config
	if postgresConnString == "" {
		postgresConnString = os.Getenv("CONTAINARIUM_POSTGRES_URL")
		// The 10.100.0.2 default is the LXC deployment's Incus-hosted Postgres;
		// the K8s runtime has no such host. Defaulting to it there makes the
		// daemon block on startup dialing an unreachable address (#1007), so
		// skip the default on K8s — an unset URL just disables the
		// (LXC-oriented) collaborator store instead.
		if postgresConnString == "" && config.Runtime != RuntimeK8s {
			postgresConnString = "postgres://containarium:containarium@10.100.0.2:5432/containarium?sslmode=disable" // #nosec G101 -- default dev credentials for local Incus container Postgres
		}
	}

	var collabStore *collaborator.Store
	if postgresConnString != "" {
		for attempt := 1; attempt <= 5; attempt++ {
			collabStore, err = gatedStore(pgGate, postgresConnString, collaborator.NewStore)
			if err == nil || pgGate.check(postgresConnString) != nil { // #2824: no per-store retries after a failed startup check
				break
			}
			if attempt < 5 {
				log.Printf("Collaborator store attempt %d/5 failed: %v (retrying in 3s)", attempt, err)
				time.Sleep(3 * time.Second)
			}
		}
		if err != nil {
			log.Printf("Warning: Failed to create collaborator store: %v. Collaborator features disabled.", err)
		} else {
			collaboratorMgr := container.NewCollaboratorManager(containerServer.GetManager(), collabStore)
			containerServer.SetCollaboratorManager(collaboratorMgr)
			log.Printf("Collaborator management service enabled")
		}
	}

	// ssh_keys registry (#2517): same Postgres pool as the collaborator
	// store. LXC runtime only — the registry, its backfill and the
	// self-host decider read the host's /home jump accounts.
	var selfHostSSHAuthz *sshauthz.SelfHostDecider
	if collabStore != nil && config.Runtime != RuntimeK8s && containerServer.GetManager() != nil {
		selfHostSSHAuthz = setupSSHKeyRegistry(context.Background(), collabStore, containerServer, config.SSHAuthzMaxKeyAge)
	}

	// Setup route persistence and Caddy sync (independent of app hosting).
	// When app hosting is enabled, routeStore/routeSyncJob are already created above.
	// When it's not, we still need them for the management route to work after VM recreation.
	caddyAdminURL := config.CaddyAdminURL
	if routeStore == nil && postgresConnString != "" && caddyAdminURL != "" && config.BaseDomain != "" {
		pool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for route store: %v", poolErr)
		} else {
			routeStore, err = app.NewRouteStore(context.Background(), pool)
			if err != nil {
				log.Printf("Warning: Failed to create route store: %v", err)
				pool.Close()
			} else {
				proxyManager := byocIngressFromEnv(wafIngressFromEnv(app.NewProxyManager(caddyAdminURL, config.BaseDomain).WithDNSChallenge(app.DNSChallengeFromEnv())))
				if err := proxyManager.EnsureServerConfig(); err != nil {
					log.Printf("Warning: Failed to ensure Caddy server config: %v", err)
				}
				if config.ProxyProtocol {
					if err := proxyManager.EnableProxyProtocol(config.ProxyProtocolTrusted); err != nil {
						log.Printf("Warning: Failed to enable PROXY protocol on Caddy: %v", err)
					} else {
						log.Printf("Caddy listener_wrappers: PROXY v2 enabled, trusted=%v", config.ProxyProtocolTrusted)
					}
				}
				if len(config.ClientIPHeaders) > 0 || len(config.TrustedProxyCIDRs) > 0 {
					if err := proxyManager.ConfigureClientIP(config.ClientIPHeaders, config.TrustedProxyCIDRs); err != nil {
						log.Printf("Warning: Failed to configure client-IP trust on Caddy: %v", err)
					} else {
						log.Printf("Caddy client-IP trust: headers=%v extra_trusted=%v", config.ClientIPHeaders, config.TrustedProxyCIDRs)
					}
				}

				// Create L4ProxyManager for TLS passthrough (SNI-based) routing.
				// L4 is activated lazily by RouteSyncJob when passthrough routes exist.
				l4ProxyManager := app.NewL4ProxyManager(caddyAdminURL)
				if config.ProxyProtocol {
					if err := l4ProxyManager.EnableL4ProxyProtocol(config.ProxyProtocolTrusted); err != nil {
						log.Printf("Warning: Failed to enable PROXY protocol on caddy-l4: %v", err)
					}
				}

				syncInterval := config.RouteSyncInterval
				if syncInterval == 0 {
					syncInterval = 5 * time.Second
				}
				routeSyncJob = app.NewRouteSyncJob(routeStore, proxyManager, syncInterval)
				routeSyncJob.SetL4ProxyManager(l4ProxyManager)
				log.Printf("Route persistence enabled (standalone) with %v sync interval", syncInterval)

				// Update NetworkServer so the /v1/network/routes API returns routes from PostgreSQL
				if networkServer != nil {
					networkServer.routeStore = routeStore
					networkServer.proxyManager = proxyManager
					networkServer.baseDomain = config.BaseDomain
					log.Printf("Network service updated with route store (standalone)")
				}
			}
		}
	}

	// Cloud-actuation client (#354): if this host is enrolled with a cloud
	// control plane, run the actuation client — heartbeat + WatchAssignments,
	// syncing each org's egress policy into the NetworkPolicyServer store
	// (closing the #315 loop), reconciling assigned containers, and exposing their
	// routes at the host edge via routeStore. Unenrolled (no cloud.yaml) → the
	// daemon is single-tenant and makes no outbound calls. Built here (not at
	// npServer setup) so the actuator has the finalized routeStore.
	cloudCfgPath := os.Getenv("CONTAINARIUM_CLOUD_CONFIG")
	if cloudCfgPath == "" {
		if p, perr := cloud.DefaultPath(); perr == nil {
			cloudCfgPath = p
		}
	}
	if cloudCfg, cerr := cloud.Load(cloudCfgPath); cerr != nil {
		log.Printf("Warning: failed to read cloud-actuation config %s: %v (running single-tenant)", cloudCfgPath, cerr)
	} else if cloudCfg != nil {
		// Keep the managed forwarder behind the same local hosted-account gate
		// used by /authorized-keys. A login for a missing/orphaned box must not
		// consume a cloud authorization call.
		localSSHFacts := sshauthz.LocalFactsFunc(func(_ context.Context, req *pb.AuthorizeSSHRequest) (string, bool, error) {
			login := strings.TrimSpace(req.GetLogin())
			if login == "" {
				return "", false, nil
			}
			mgr := containerServer.GetManager()
			if mgr == nil {
				return "", false, fmt.Errorf("container manager unavailable")
			}
			names, err := mgr.ExistingContainerNames()
			if err != nil {
				return "", false, err
			}
			if names[login+"-container"] {
				return login, true, nil
			}
			if collaboratorContainer, ok := container.CollaboratorJumpAccountContainer(login); ok && names[collaboratorContainer] {
				return login, true, nil
			}
			return "", false, nil
		})
		// Managed AuthorizeSSH ALLOWs record (key, control-plane user id,
		// login) here; TERMINATE_USER_SESSIONS resolves through it (#2602).
		managedSSHIdentities := sshauthz.NewManagedIdentities()
		cloudDeps := cloud.Deps{
			Policies: newCloudPolicySink(npServer),
			// Report this host's capability profile (hardware + headroom +
			// version) so the BYO fleet view goes live (#528).
			// Policy-store readiness (#2483) rides along as advisory checks.
			Status: cloud.DefaultStatusProbe{Readiness: policyreadiness.ServerCheck(policyStoreHealth)},
			// TERMINATE_USER_SESSIONS assignments run the same function as
			// `containarium sessions terminate` (#2522).
			// The control-plane user id an assignment names is resolved
			// through managedSSHIdentities (#2602).
			Sessions: cloudSessionTerminator{s: containerServer, ids: managedSSHIdentities},
		}
		if cloudActuator, actErr := newCloudContainerActuator(routeStore); actErr != nil {
			log.Printf("Warning: cloud container actuator unavailable (%v); policy sync only", actErr)
		} else {
			cloudDeps.Containers = cloudActuator // only set when non-nil (avoid nil-iface trap)
		}
		// Driver-token auto-refresh (#557). Gating this on cloud.yaml's
		// jwt_secret_file alone turned out to be a production trap
		// (cloud#888/#903 postmortem): a cloud.yaml written by a pre-#557
		// enroll has no jwt_secret_file, nothing backfills the config on
		// daemon upgrades, and the refresh loop silently never armed — so
		// legacy-enrolled hosts' cloud-stored driver tokens all died at the
		// 30-day cap and every cloud→host driver call 401'd forever. When
		// the config doesn't name a secret file, fall back to the default
		// daemon secret path if it's readable; only an explicit
		// driver_token_disabled opts out of refresh entirely.
		switch secretFile := cloud.ResolveDriverSecretFile(cloudCfg, nil); {
		case secretFile != "":
			if cloudCfg.JWTSecretFile == "" {
				log.Printf("Cloud driver-token refresh: cloud.yaml has no jwt_secret_file (legacy enroll); falling back to %s", secretFile)
			}
			sf := secretFile // capture for closure
			cloudDeps.Driver = func() (string, error) {
				return cloud.MintDriverToken(sf, 30*24*time.Hour)
			}
		case !cloudCfg.DriverTokenDisabled:
			log.Printf("WARNING: cloud driver-token refresh DISABLED (no jwt_secret_file in cloud.yaml and no readable %s) — the cloud-stored driver token will expire within 30 days and cloud→host operations will start failing 401; re-run `containarium cloud enroll` to fix", cloud.DefaultDaemonJWTSecretFile)
		}
		if cc, nerr := cloud.New(cloudCfg, cloudDeps); nerr != nil {
			log.Printf("Warning: cloud-actuation config invalid: %v (running single-tenant)", nerr)
		} else {
			cloudClient = cc
			// Enrollment is the mode switch for managed SSH authorization. The
			// server is registered above, but the forwarder is not constructed
			// at all on an unenrolled daemon.
			sshAuthzServer.SetDecider(&sshauthz.Decider{Local: localSSHFacts, Managed: true, Cloud: cc, Identities: managedSSHIdentities})
			log.Printf("Cloud-actuation client configured (host=%s control-plane=%s); starts with the daemon",
				cloudCfg.HostID, cloudCfg.ControlPlane)
		}
	}

	// Self-host SSH authorization (#2517): only when no cloud is enrolled.
	// An enrolled daemon's answer comes from the cloud (#2525) through the
	// managed forwarder wired above; it never decides locally.
	if selfHostSSHAuthz != nil && cloudClient == nil {
		sshAuthzServer.SetDecider(selfHostSSHAuthz)
		log.Printf("SSH authorization: self-host decider enabled (max key age %v, 0 = no limit)", config.SSHAuthzMaxKeyAge)
	}

	// Setup passthrough route persistence and iptables sync.
	// This mirrors the route store pattern but for TCP/UDP passthrough routes.
	var passthroughStore network.PassthroughStore
	var passthroughSyncJob *network.PassthroughSyncJob
	if postgresConnString != "" && networkServer != nil {
		pool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for passthrough store: %v", poolErr)
		} else {
			passthroughStore, err = network.NewPassthroughStore(context.Background(), pool)
			if err != nil {
				log.Printf("Warning: Failed to create passthrough store: %v", err)
				pool.Close()
			} else {
				syncInterval := config.RouteSyncInterval
				if syncInterval == 0 {
					syncInterval = 5 * time.Second
				}
				passthroughSyncJob = network.NewPassthroughSyncJob(passthroughStore, networkServer.passthroughManager, syncInterval)
				networkServer.passthroughStore = passthroughStore
				// ContainerServer also needs the passthrough store so
				// DeleteContainer can cascade-clean a recipe-deployed box's
				// TCP/UDP passthrough routes (#1462), mirroring
				// SetRouteCleanupDeps above for HTTP routes.
				containerServer.SetPassthroughCleanupDep(passthroughStore)
				log.Printf("Passthrough route persistence enabled with %v sync interval", syncInterval)
			}
		}
	}

	// Upgrade the NetworkPolicy service from its initial in-memory store to a
	// Postgres-backed one now that postgresConnString is finalized. Best-effort:
	// on any failure we keep the in-memory store (policies won't survive a
	// restart, but the service stays up). Swap happens before grpcServer.Serve,
	// so it races with no live RPCs.
	// npServer's durable flag says whether its store is the source of truth
	// for network policies: true with no Postgres configured (the in-memory
	// store IS the store) or once the Postgres store is installed; false
	// while Postgres was configured but could not be reached, because the
	// in-memory fallback is then empty and must not be read as "no policies"
	// (#2359 — the tenant guard would strip persisted allow_from_tenants).
	// While false, the operator mutation RPCs refuse with UNAVAILABLE (#3131).
	npServer.SetDurable(postgresConnString == "")
	// The coding-tool egress policy store (#2378) gets the same guard: with
	// Postgres configured it refuses every RPC until its Postgres store is
	// installed below, instead of answering "unrestricted" from the empty
	// in-memory stand-in.
	codeEgressServer.SetDurable(postgresConnString == "")

	// The Postgres guardrail policy store (#2368), or nil if it is not
	// installed below; guardrailPolicyStartupStore turns nil into fail-closed.
	var guardrailPGStore guardrailpolicy.Store
	var retentionPGStore retention.Store
	// The pool the dry-run sweeper counts through (#2876); nil without one.
	var retentionSweepPool *pgxpool.Pool
	// The audit_logs prefix purge (#2878); only built once there is an audit store.
	var retentionChainSource *retentionpg.ChainSource
	// The startup pool, handed to the policy-store recovery retry (#2483)
	// when a store's bootstrap failed over a pool that did connect.
	var policyStorePool *pgxpool.Pool
	if postgresConnString != "" {
		pool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for network policy store: %v", poolErr)
		} else {
			policyStorePool = pool
			// Wired independently of the network-policy store below, so a
			// failure there does not leave this one on the stand-in.
			if ceErr := codeEgressServer.InstallDurableStore(func() (CodingToolEgressPolicyStore, error) {
				return NewPostgresCodingToolEgressPolicyStore(context.Background(), pool)
			}); ceErr != nil {
				log.Printf("Warning: Failed to create Postgres coding-tool egress policy store (its RPCs fail UNAVAILABLE): %v", ceErr)
			} else {
				log.Printf("Coding-tool egress policy persistence enabled (Postgres store)")
			}
			// Managed-cluster state (#1413) is wired FIRST and
			// independently: a network-policy store failure below must
			// not silently leave clusters on the in-memory store (the
			// pool therefore stays open even if the netpol store fails).
			// Best-effort like its siblings: on failure the in-memory
			// store stays and the daemon comes up either way.
			if clStore, cErr := clusterstore.NewPGStore(context.Background(), pool); cErr != nil {
				log.Printf("Warning: Failed to create Postgres cluster store: %v", cErr)
			} else {
				clusterServer.SetStore(clStore)
				log.Printf("Managed-cluster persistence enabled (Postgres store)")
			}

			// Guardrail policy (#2368), independent of the netpol store below.
			if gpStore, gErr := guardrailpolicypg.New(context.Background(), pool); gErr != nil {
				log.Printf("Warning: Failed to create Postgres guardrail policy store: %v", gErr)
			} else {
				guardrailPGStore = gpStore
				log.Printf("Guardrail policy persistence enabled (Postgres store)")
			}
			// Retention policy and legal holds (#2875), independent of the rest.
			if rStore, rErr := retentionpg.New(context.Background(), pool); rErr != nil {
				log.Printf("Warning: Failed to create Postgres retention store: %v", rErr)
			} else {
				retentionPGStore = rStore
				retentionSweepPool = pool
			}

			if pgStore, npErr := NewPostgresNetworkPolicyStore(context.Background(), pool); npErr != nil {
				log.Printf("Warning: Failed to create Postgres network policy store: %v", npErr)
			} else {
				npServer.InstallDurableStore(pgStore)
				log.Printf("NetworkPolicy persistence enabled (Postgres store)")
				// Operator signatures (#661 PR-B) share the same pool.
				if sigStore, sErr := NewPostgresNetworkPolicySignatureStore(context.Background(), pool); sErr != nil {
					log.Printf("Warning: Failed to create Postgres network-policy signature store: %v", sErr)
				} else {
					npServer.SetSignatureStore(sigStore)
					log.Printf("NetworkPolicy signature persistence enabled (Postgres store)")
				}

				// Agent run state (#1182) shares the same pool. Same best-effort
				// posture: on failure the in-memory store stays, so the daemon comes
				// up either way — it just loses runs across a restart, which is the
				// behavior that predates this.
				if runStore, rErr := NewPostgresCrewRunStore(context.Background(), pool); rErr != nil {
					log.Printf("Warning: Failed to create Postgres crew-run store: %v", rErr)
				} else {
					crewServer.SetRunStore(runStore)
					crewServer.SetOwner(config.LocalBackendID)
					log.Printf("Crew-run persistence enabled (Postgres store)")
					// Reconcile runs the previous daemon was driving when it
					// stopped. RunCrew records a run as RUNNING before driving it,
					// so without this they stay RUNNING forever now that the state
					// is durable — GetCrewRun would answer RUNNING for a run that
					// can never finish (#1182 AC4). Best-effort: a daemon that
					// cannot reconcile should still start.
					if n, fErr := runStore.FailStranded(context.Background(), config.LocalBackendID, StrandedByRestart); fErr != nil {
						log.Printf("Warning: could not reconcile stranded crew runs: %v", fErr)
					} else if n > 0 {
						log.Printf("Marked %d crew run(s) FAILED: in flight when the previous daemon stopped", n)
					}
				}
				if taskQueue, qErr := NewPostgresAgentTaskQueue(context.Background(), pool); qErr != nil {
					log.Printf("Warning: Failed to create Postgres agent task queue: %v", qErr)
				} else {
					agentSkillServer.SetTaskQueue(taskQueue)
					log.Printf("Agent task-queue persistence enabled (Postgres store)")
				}
			}
		}
	}
	guardrailPolicyServer.SetStore(guardrailPolicyStartupStore(postgresConnString != "", guardrailPGStore))
	retentionStore := retentionStartupStore(postgresConnString != "", retentionPGStore)
	retentionServer.SetStore(retentionStore)
	// The dry-run sweeper (#2876): counts only, started in Start.
	retentionReporter := newRetentionReporter(retentionStore, retentionSweepPool)
	retentionServer.SetReporter(retentionReporter)
	// Either policy store still on its fail-closed stand-in is retried in the
	// background (5 s doubling to 60 s) until Postgres answers; the install
	// is an atomic swap, and Provider() below follows it (#2483).
	policyStoreRetry := newPolicyStoreRecovery(guardrailPolicyServer, codeEgressServer, policyStoreHealth, postgresConnString)
	policyStoreRetry.syncHealth()
	if policyStoreRetry.pending() {
		log.Printf("Policy stores: retrying the Postgres store install every %s, up to every %s, until it succeeds", policyStoreRetryInitial, policyStoreRetryMax)
		go policyStoreRetry.Run(context.Background(), policyStorePool)
	}
	// The deploy gate reads the same store (after the swap above).
	recipeServer.SetGuardrailGate(guardrailPolicyServer.Provider(), config.GuardrailStagingRoot, config.GuardrailSnapshotDir)
	if config.GuardrailStagingRoot == "" {
		log.Printf("Guardrail gate: no --guardrail-staging-root; guardrail-gated recipe deploys are refused")
	}
	recipeServer.SetGuardrailDeployRateLimit(guardrailrate.New("gated deploy", config.GuardrailRateLimits.Deploy))
	guardrailPolicyServer.SetRateLimits(
		guardrailrate.New("guardrail policy set", config.GuardrailRateLimits.PolicySet),
		guardrailrate.New("guardrail policy get", config.GuardrailRateLimits.PolicyGet))

	// Managed-cluster reconciler (#1414): converges cluster records into
	// control-plane + worker VMs (pure Decide policy in
	// pkg/core/cluster). Wired AFTER the Postgres store swap so it
	// shares whichever store the cluster server ended up on. Set
	// CONTAINARIUM_CLUSTER_RECONCILER=false to disable.
	if os.Getenv("CONTAINARIUM_CLUSTER_RECONCILER") != "false" {
		if clusterIncus, cErr := incus.New(); cErr != nil {
			log.Printf("[cluster] reconciler disabled: incus unavailable: %v", cErr)
		} else {
			clusterMgr := clustercore.NewManager(clustercore.NewIncusHost(clusterIncus), clustercore.DefaultArtifactBase)
			clusterReconciler := NewClusterReconciler(clusterServer.Store(), clusterMgr)
			clusterReconciler.SetAdmission(containerServer.admitCPUCapacity)
			if adv := os.Getenv("CONTAINARIUM_CLUSTER_ADVERTISE_ADDR"); adv != "" && networkServer != nil {
				portRange := os.Getenv("CONTAINARIUM_CLUSTER_PORT_RANGE")
				if portRange == "" {
					portRange = "36443-36542"
				}
				if pErr := clusterReconciler.WireEndpointPublisher(networkServer, adv, portRange); pErr != nil {
					log.Printf("[cluster] endpoint publisher disabled: %v", pErr)
				}
			} else {
				log.Printf("[cluster] CONTAINARIUM_CLUSTER_ADVERTISE_ADDR unset; cluster API endpoints use VM IPs (host-local reach only)")
			}
			if os.Getenv("CONTAINARIUM_CLUSTER_VPA") == "false" {
				clusterReconciler.SetVPADisabled(true)
				log.Printf("[cluster] VPA deployment disabled by CONTAINARIUM_CLUSTER_VPA=false")
			}
			// CA-provider mTLS surface (#1415, transport option 1):
			// per-cluster client certificates — the stock externalgrpc
			// client's only auth mechanism. Requires the advertise
			// address cluster VMs reach the daemon on; without it,
			// clusters run without an autoscaler (reconciler still
			// converges each group to its min).
			if caAdvertise := os.Getenv("CONTAINARIUM_CLUSTER_CA_ADVERTISE"); caAdvertise != "" {
				pkiDir := os.Getenv("CONTAINARIUM_CLUSTER_CA_PKI_DIR")
				if pkiDir == "" {
					pkiDir = "/etc/containarium/cluster-ca"
				}
				caListen := os.Getenv("CONTAINARIUM_CLUSTER_CA_LISTEN")
				if caListen == "" {
					caListen = "0.0.0.0:36442"
				}
				if pki, pkiErr := LoadOrCreateClusterPKI(pkiDir); pkiErr != nil {
					log.Printf("[cluster] autoscaler disabled: cluster PKI: %v", pkiErr)
				} else {
					sanHost := caAdvertise
					if h, _, splitErr := net.SplitHostPort(caAdvertise); splitErr == nil {
						sanHost = h
					}
					caProvider := NewCAProviderServer(clusterServer.Store(), clusterMgr)
					if _, _, lErr := StartClusterCAListener(caListen, []string{sanHost}, pki, caProvider); lErr != nil {
						log.Printf("[cluster] autoscaler disabled: CA-provider listener: %v", lErr)
					} else {
						clusterReconciler.SetCADeployer(caAdvertise, func(owner, name string) (clustercore.CACredentials, error) {
							cert, key, mErr := pki.MintClientCert(owner, name)
							if mErr != nil {
								return clustercore.CACredentials{}, mErr
							}
							return clustercore.CACredentials{ClientCertPEM: cert, ClientKeyPEM: key, CACertPEM: pki.CAPEM()}, nil
						})
						log.Printf("Managed-cluster autoscaler enabled (provider %s)", caAdvertise)
						// Opt-in scale-down timer overrides (test lanes); unset = stock.
						// A malformed value is logged and ignored rather than
						// fatal: it only ever shortens a wait.
						if delay, unneeded, sdErr := parseCAScaleDownEnv(os.Getenv); sdErr != nil {
							log.Printf("[cluster] ignoring autoscaler scale-down override: %v", sdErr)
						} else if delay > 0 || unneeded > 0 {
							clusterReconciler.SetCAScaleDown(delay, unneeded)
							log.Printf("[cluster] autoscaler scale-down timers overridden: delay-after-add=%v unneeded-time=%v (0 = stock)", delay, unneeded)
						}
					}
				}
			} else {
				log.Printf("[cluster] CONTAINARIUM_CLUSTER_CA_ADVERTISE unset; clusters run without an autoscaler")
			}

			clusterServer.SetReconciler(clusterReconciler)
			go clusterReconciler.Run(context.Background())
			log.Printf("Managed-cluster reconciler enabled")
		}
	}

	reflection.Register(grpcServer)

	// Create OTel metrics collector if VictoriaMetrics URL is available
	var metricsCollector *metrics.Collector
	if config.VictoriaMetricsURL != "" {
		metricsIncusClient, err := incus.New()
		if err != nil {
			log.Printf("Warning: Failed to create incus client for metrics: %v", err)
		} else {
			collectorConfig := metrics.DefaultCollectorConfig()
			collectorConfig.VictoriaMetricsURL = config.VictoriaMetricsURL
			collectorConfig.LocalBackendID = config.LocalBackendID
			mc, err := metrics.NewCollector(collectorConfig, metricsIncusClient)
			if err != nil {
				log.Printf("Warning: Failed to create OTel metrics collector: %v", err)
			} else {
				metricsCollector = mc
				log.Printf("OTel metrics collector configured (target: %s)", config.VictoriaMetricsURL)

				// Per-stage create latency (Phase 0 of
				// docs/architecture/two-digit-ms-sandbox-spawn.md).
				if obs, err := metrics.NewCreateStageObserver(mc.MeterProvider()); err != nil {
					log.Printf("Warning: create-stage latency instrumentation disabled: %v", err)
				} else {
					containerServer.GetManager().SetStageObserver(obs)
				}
			}
		}
	}

	// Storage pool capacity log + gauges, on every daemon (#2823).
	if m := startStoragePoolMonitor(context.Background(), metricsCollector, config.LocalBackendID); m != nil {
		containerServer.SetStoragePoolSource(m.Snapshot)
		// Headroom floor (#3128), checked against the same snapshot.
		containerServer.SetPoolHeadroomGuard(poolcapacity.NewGuard(config.PoolHeadroomFloors, m.Snapshot, incus.ConfiguredStoragePool, nil))
		if len(config.PoolHeadroomFloors) > 0 {
			log.Printf("[storage] pool headroom floors: %s (other pools: %d%%)", config.PoolHeadroomFloors, poolcapacity.DefaultFloorPercent)
		}
	} else {
		log.Printf("WARN: storage pool headroom floor disabled: pool sizes cannot be read, so creates and snapshots are not checked (#3128)")
	}

	// Set monitoring URLs on container server so GetMonitoringInfo works.
	// Grafana is served via reverse proxy at /grafana/ on the same host,
	// so the public URL is relative to the server's own origin.
	if config.VictoriaMetricsURL != "" {
		// The web UI derives the full Grafana URL from its own origin + /grafana/
		grafanaURL := "/grafana"
		containerServer.SetMonitoringURLs(config.VictoriaMetricsURL, grafanaURL)
	}

	// Setup ClamAV security scanner
	var securityScanner *security.Scanner
	var securityStore *security.Store
	var securityServerInstance *SecurityServer
	if postgresConnString != "" && !config.DisableSecurityScanner {
		securityPool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for security store: %v", poolErr)
		} else {
			securityStore, err = security.NewStore(context.Background(), securityPool)
			if err != nil {
				log.Printf("Warning: Failed to create security store: %v", err)
				securityPool.Close()
			} else {
				// Register SecurityService gRPC handler
				securityIncusClient, incusErr := incus.New()
				if incusErr != nil {
					log.Printf("Warning: Failed to create incus client for security: %v", incusErr)
				} else {
					// Create scanner first so we can pass it to the server
					securityScanner = security.NewScanner(securityStore, securityIncusClient)

					// Auto-quarantine (#659): on malware detection, block the
					// tenant's egress via a deny rule; release on a clean scan.
					// Opt-in — the quarantine only bites when the network-policy BPF
					// enforcer is also armed. Uses npServer's deny-rule store directly
					// (in-process, no RPC/auth).
					switch strings.ToLower(strings.TrimSpace(os.Getenv("CONTAINARIUM_SECURITY_AUTO_QUARANTINE"))) {
					case "1", "true", "yes", "on":
						aq := NewAutoQuarantine(npServer.Store())
						securityScanner.SetScanResultHook(aq.OnScanResult)
						log.Printf("Security auto-quarantine enabled: malware-infected containers will have their tenant's egress blocked (release on clean scan)")
					}

					securityServerInstance = NewSecurityServer(securityStore, securityIncusClient, securityScanner)
					pb.RegisterSecurityServiceServer(grpcServer, securityServerInstance)
					log.Printf("Security service enabled")

					// Ensure security container exists (background, non-blocking)
					go func() {
						coreServices := NewCoreServices(securityIncusClient, CoreServicesConfig{
							NetworkCIDR: networkCIDR,
						})
						if err := coreServices.EnsureSecurity(context.Background()); err != nil {
							log.Printf("Warning: Failed to setup security container: %v. ClamAV scanning disabled.", err)
						} else {
							log.Printf("Security container ready")
						}
					}()
				}
			}
		}
	}

	// Setup pentest manager
	var pentestManager *pentest.Manager
	var pentestServerRef *PentestServer // armed with the audit store once it exists

	var pentestStore *pentest.Store
	if postgresConnString != "" && !config.DisablePentestScanner {
		pentestPool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for pentest store: %v", poolErr)
		} else {
			pentestStore, err = pentest.NewStore(context.Background(), pentestPool)
			if err != nil {
				log.Printf("Warning: Failed to create pentest store: %v", err)
				pentestPool.Close()
			} else {
				pentestIncusClient, incusErr := incus.New()
				if incusErr != nil {
					log.Printf("Warning: Failed to create incus client for pentest: %v", incusErr)
				} else {
					var meterProvider *sdkmetric.MeterProvider
					if metricsCollector != nil {
						meterProvider = metricsCollector.MeterProvider()
					}
					pentestManager = pentest.NewManager(
						pentestStore,
						pentestIncusClient,
						routeStore,
						meterProvider,
						pentest.ManagerConfig{},
					)
					pentestServer := NewPentestServer(pentestStore, pentestManager)
					pentestServerRef = pentestServer
					pb.RegisterPentestServiceServer(grpcServer, pentestServer)
					log.Printf("Pentest service enabled")
				}
			}
		}
	}

	// Setup ZAP scanner manager
	var zapManager *zapscanner.Manager
	var zapStore *zapscanner.Store
	if postgresConnString != "" && !config.DisableZapScanner {
		zapPool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for ZAP store: %v", poolErr)
		} else {
			zapStore, err = zapscanner.NewStore(context.Background(), zapPool)
			if err != nil {
				log.Printf("Warning: Failed to create ZAP store: %v", err)
				zapPool.Close()
			} else {
				zapManager = zapscanner.NewManager(
					zapStore,
					routeStore,
					zapscanner.ManagerConfig{},
				)
				zapServer := NewZapServer(zapStore, zapManager)
				pb.RegisterZapServiceServer(grpcServer, zapServer)
				log.Printf("ZAP service enabled")
			}
		}
	}

	// Setup audit logging store and event subscriber
	var auditStore *audit.Store
	var modelGW *modelgateway.Gateway // set when the model gateway is enabled; drained on shutdown
	var auditEventSubscriber *audit.EventSubscriber
	var revocationStoreLocal *auth.PgRevocationStore
	var ownerRevocationStoreLocal *auth.PgOwnerRevocationStore
	if postgresConnString != "" {
		auditPool, poolErr := pgGate.connect(postgresConnString, 5, 3*time.Second)
		if poolErr != nil {
			log.Printf("Warning: Failed to connect to PostgreSQL for audit store: %v", poolErr)
		} else {
			auditStore, err = audit.NewStore(context.Background(), auditPool)
			if err != nil {
				log.Printf("Warning: Failed to create audit store: %v", err)
				auditPool.Close()
			} else {
				auditEventSubscriber = audit.NewEventSubscriber(events.GetBus(), auditStore)
				// #1605 — arms the native gRPC server's audit interceptor,
				// which was registered on grpcServer above (before this
				// store existed) and has been a no-op until now.
				auditGRPCInterceptor.SetStore(auditStore)
				codeEgressServer.SetAuditStore(auditStore)
				if pentestServerRef != nil {
					pentestServerRef.SetAuditStore(auditStore)
				}
				// #2415 — the sentinel ships its SSH session records here.
				// Registered only when the store exists; without Postgres the
				// RPC is simply absent (Unimplemented) rather than a stub.
				pb.RegisterAuditServiceServer(grpcServer, NewAuditServer(auditStore))
				// #3064 — verify a sealed Proof Pack against this chain.
				pb.RegisterProofPackServiceServer(grpcServer, NewProofPackServer(auditStore))
				log.Printf("Audit logging service enabled")
			}

			// Phase 1.2 — JWT revocation list. Same pool as
			// audit so we don't open a second connection just
			// for one hot-path lookup. Cleanup goroutine is
			// launched from Start() so its lifetime tracks
			// the daemon's serving context.
			revStore, revErr := auth.NewPgRevocationStore(context.Background(), auditPool)
			if revErr != nil {
				log.Printf("Warning: Failed to create JWT revocation store: %v", revErr)
			} else {
				tokenManager.SetRevocationStore(revStore)
				revocationStoreLocal = revStore
				log.Printf("JWT revocation list enabled")

				// Phase 1.2 follow-up — TokensService RPC
				// for operator revocation via CLI / MCP.
				tokensServer := NewTokensServer(tokenManager, revStore, 0)
				pb.RegisterTokensServiceServer(grpcServer, tokensServer)
				log.Printf("TokensService registered (POST /v1/tokens/revoke, /v1/tokens/delegate)")
			}

			// #2111 — the model gateway's owner-level kill-switch, durable:
			// one cutoff row per key owner, so a daemon restart no longer
			// forgets that an owner's key was removed. Same pool as the jti
			// list. Handed to the gateway through gatewayOwnerRevocations,
			// which carries the nil-pointer-in-interface check.
			ownerRevStore, ownerRevErr := auth.NewPgOwnerRevocationStore(context.Background(), auditPool)
			if ownerRevErr != nil {
				log.Printf("Warning: Failed to create model-gateway owner revocation store: %v", ownerRevErr)
			} else {
				ownerRevocationStoreLocal = ownerRevStore
			}
		}
	}

	// Run-lease revocation for skill runs (#1817): a run's credentials die when
	// the run does. Same store the model-gateway's per-call jti check and
	// `containarium token revoke` already use — revocation is issuer-agnostic,
	// so one store covers the platform JWT and the gateway token alike.
	//
	// Assigned through a nil check rather than directly: a nil
	// *auth.PgRevocationStore placed in an interface field yields a NON-nil
	// interface holding a nil pointer, so runlease's `rev == nil` guard would not
	// fire and every run exit would dereference nil. A daemon without Postgres
	// reaches here with a nil store, so this is the common path, not a corner
	// case. (Same hazard, same shape, as the gateway's wiring below.)
	//
	// Wired HERE, not inside the model-gateway block below: the platform JWT is
	// minted for every skill run, gateway or not, so a daemon with no provider
	// key — and a daemon with REST disabled — must still be able to revoke it.
	if revocationStoreLocal != nil {
		agentSkillServer.SetRevocationStore(revocationStoreLocal)
	} else {
		log.Printf("Warning: agent skill runs have no revocation store (no Postgres) — a run's credentials cannot be killed before their %s expiry; seed files are still wiped at run exit", agentTokenTTL)
	}

	// Setup SSH login collector. Which source it reads depends on the box
	// backend: an LXC box keeps sessions in /var/log/auth.log and is read by
	// exec, a K8s box logs them to stderr and is read from the pod log
	// (#1189). Before this, the collector was built only when an incus client
	// could be created, so a K8s deployment recorded no logins at all — which
	// reads exactly like a fleet nobody logged into.
	var sshCollector *audit.SSHCollector
	if auditStore != nil {
		if containerServer != nil && containerServer.boxes().Kind() == box.KindK8s {
			if backend, ok := containerServer.boxes().(k8sClientsetProvider); ok {
				sshCollector = audit.NewSSHCollectorWithSource(
					audit.NewK8sSessionSource(backend.Clientset(), ""), auditStore)
				log.Printf("SSH login collector configured (K8s pod logs)")
			} else {
				log.Printf("Warning: K8s box backend exposes no clientset; SSH logins will not be audited")
			}
		} else if sshIncusClient, incusErr := incus.New(); incusErr != nil {
			log.Printf("Warning: Failed to create incus client for SSH collector: %v", incusErr)
		} else {
			sshCollector = audit.NewSSHCollector(sshIncusClient, auditStore)
			log.Printf("SSH login collector configured (LXC auth.log)")
		}
	}

	// Setup the eBPF network-policy enforcer (#315 Phase A). OFF by default:
	// only constructed when CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT points at a
	// compiled netpolicy.bpf.o. When unset, an existing deployment is entirely
	// unaffected. Observation-only — the program never drops, it audits
	// would-deny flows. The tenant→u32 ID registry is persisted in Postgres when
	// available (IDs must be stable across restarts) and in-memory otherwise.
	var networkPolicyEnforcer *NetworkPolicyEnforcer
	netCfg := appconfig.LoadNetwork()
	// GetNetworkPolicyStatus reports this host's guard whether or not an
	// enforcer is configured.
	npServer.SetEnforcementArmed(netCfg.PolicyEnforce)
	if bpfObj := netCfg.PolicyBPFObject; bpfObj != "" && networkIncusClient != nil {
		var tenantRegistry TenantRegistry
		if postgresConnString != "" {
			if regPool, perr := pgGate.connect(postgresConnString, 5, 3*time.Second); perr != nil {
				log.Printf("Warning: tenant registry Postgres connect failed (%v); using in-memory IDs", perr)
				tenantRegistry = NewMemTenantRegistry()
			} else if pgReg, rerr := NewPostgresTenantRegistry(context.Background(), regPool); rerr != nil {
				log.Printf("Warning: tenant registry init failed (%v); using in-memory IDs", rerr)
				regPool.Close()
				tenantRegistry = NewMemTenantRegistry()
			} else {
				tenantRegistry = pgReg
			}
		} else {
			tenantRegistry = NewMemTenantRegistry()
		}
		// Second opt-in: enforcement (packet drops) only happens when the operator
		// also arms it. Without this, even a stored `--mode enforce` policy stays
		// observation-only — so an operator soaks in log_only, watches the
		// would-deny logs, finishes the allow-list, then arms enforce.
		enforceArmed := netCfg.PolicyEnforce
		networkPolicyEnforcer = NewNetworkPolicyEnforcer(bpfObj, npServer.Store(), tenantRegistry, fullListInspector{networkIncusClient}, auditStore, events.GetBus(), enforceArmed)
		networkPolicyEnforcer.SetIncusDataDir(networkIncusClient.DataDir()) // DHCP leases live under it (#2618)
		// Status reads what the enforcer loaded; it reports "not running" on its
		// own until Start succeeds, so a failed start needs no unwiring.
		npServer.SetAppliedStateSource(networkPolicyEnforcer)
		// Tier 2 (#661): opt into inbound cleartext exploit-signature scanning.
		// Separate from ENFORCE — loading signatures is harmless in observation
		// mode (logs matches), but the per-packet scan cost only runs when set.
		sigArmed := netCfg.PolicySignatures
		// Egress preset (#2440): the bridge gateway is the box's DNS resolver, which a
		// preset policy allows implicitly so an allow-list cannot blackhole resolution.
		if gw, gerr := netip.ParseAddr(bridgeGatewayIP(networkCIDR)); gerr == nil {
			networkPolicyEnforcer.SetImplicitGateway(gw)
		} else {
			log.Printf("NetworkPolicy: cannot derive the bridge gateway from %q; egress presets will not implicitly allow the resolver", networkCIDR)
		}
		// Plan (#2440) reads the same deny audit rows the enforcer writes.
		if auditStore != nil {
			planGW, _ := netip.ParseAddr(bridgeGatewayIP(networkCIDR))
			npServer.SetPlanSources(auditStore, planGW)
		}
		// Default egress posture (#2577, stage 1): a tenant with no stored policy
		// runs the default allow-list in log_only instead of an empty one, so the
		// would-deny log shows real surprises. Observe-only; never arms drops.
		configureDefaultEgress(networkPolicyEnforcer, netCfg.DefaultEgress, netCfg.DefaultEgressDomains, netCfg.DefaultEgressCIDRs)
		networkPolicyEnforcer.SetSignaturesEnabled(sigArmed)
		networkPolicyEnforcer.SetSignatureStore(npServer.SignatureStore()) // #661 PR-B: merge operator signatures
		if enforceArmed {
			log.Printf("NetworkPolicy enforcer configured (obj=%s); ENFORCE ARMED — enforce-mode policies will drop packets", bpfObj)
		} else {
			log.Printf("NetworkPolicy enforcer configured (obj=%s); observation-only (set CONTAINARIUM_NETWORK_POLICY_ENFORCE=1 to arm drops)", bpfObj)
		}
		if sigArmed {
			log.Printf("NetworkPolicy Tier 2 signature scanning enabled (CONTAINARIUM_NETWORK_POLICY_SIGNATURES=1)")
		}
	}

	// Core-infra network guard (#2084, docs/architecture/core-infra-network-guard.md):
	// one Incus NIC ACL per core-role container, ingress default-drop with the
	// role→listener allow table, so tenants on the shared bridge cannot reach
	// core-postgres / grafana / the Caddy admin API at the network layer.
	// OFF unless CONTAINARIUM_CORE_GUARD=enforce; constructed regardless so
	// its status can say "off" instead of the guard simply not existing.
	// networkCIDR is the bridge's ipv4.address in gateway form as incus
	// reports it, which is exactly what the reconciler needs.
	var coreGuard *coreguard.Reconciler
	if networkIncusClient != nil {
		coreGuard = coreguard.NewReconciler(networkIncusClient, coreguard.Config{
			Mode:       coreguard.ParseMode(netCfg.CoreGuard),
			Bridge:     "incusbr0",
			BridgeCIDR: networkCIDR,
		})
	}

	// Tenant network guard (#2347, docs/architecture/tenant-network-guard.md):
	// one Incus NIC ACL per tenant, ingress default-drop with host gateway +
	// core initiators + same-tenant addresses allowed, so tenants on the
	// shared bridge cannot reach each other. ON unless
	// CONTAINARIUM_TENANT_GUARD=off; also consulted at container birth so a
	// new box is guarded before it starts.
	var tenantGuard *tenantguard.Reconciler
	if networkIncusClient != nil {
		tenantGuard = tenantguard.NewReconciler(networkIncusClient, tenantguard.Config{
			Mode:       nicguard.ParseMode(netCfg.TenantGuard),
			Bridge:     "incusbr0",
			BridgeCIDR: networkCIDR,
			// allow_from_tenants (#2359) comes from the network-policy store;
			// read through npServer each pass because the store is swapped to
			// Postgres after construction.
			Policies: tenantguard.PolicySourceFunc(func(ctx context.Context) ([]*pb.NetworkPolicy, error) {
				if !npServer.Durable() {
					// An empty fallback is not "no policies": fail the pass
					// so the ACLs from the last good pass stay in force.
					return nil, fmt.Errorf("network-policy store is the in-memory fallback (Postgres unavailable at startup); keeping existing ACLs")
				}
				return npServer.Store().List(ctx)
			}),
		})
		if mgr := containerServer.GetManager(); mgr != nil {
			mgr.SetNICGuard(tenantGuard)
		}
	}
	if networkServer != nil {
		networkServer.SetGuards(coreGuard, tenantGuard)
