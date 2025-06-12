//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/mtls"
	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/internal/server"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	"github.com/spf13/cobra"
)

// daemonGuardrailStagingRoot is where guardrail-gated recipe deploys read
// staged datasets from (#2368). Empty = gated deploys are refused.
var daemonGuardrailStagingRoot string

// daemonGuardrailSnapshotDir is where the gate makes its private
// verification snapshots ("" = the OS temp dir).
var daemonGuardrailSnapshotDir string

var (
	daemonAddress          string
	daemonPort             int
	daemonHTTPPort         int
	runJournalRetention    time.Duration
	runCheckoutMaxAge      time.Duration
	sshAuthzMaxKeyAge      time.Duration
	sshAuthzKeysCommand    bool
	enableMTLS             bool
	enableREST             bool
	daemonCertsDir         string
	jwtSecret              string
	jwtSecretFile          string
	swaggerDir             string
	networkSubnet          string
	skipInfraInit          bool
	requireIsolatedStorage bool
	standaloneMode         bool
	disableSecurityScanner bool
	disablePentestScanner  bool
	disableZapScanner      bool
	enableAppHosting       bool
	postgresConnString     string
	baseDomain             string
	anonClaimURLBase       string
	anonReminderWebhook    string
	anonMaxBoxes           int
	anonKeyCreatesPer10    int
	anonKeyBurst           int
	anonIPCreatesPer10     int
	anonIPBurst            int
	anonDoorStatePath      string
	caddyAdminURL          string
	caddyCertDir           string
	alertWebhookURL        string
	alertWebhookSecret     string
	sentinelURL            string
	sshHost                string
	peerAddrs              []string
	localBackendID         string
	pool                   string
	storagePool            string
	zfsTenantRoot          string
	zfsKeysDir             string
	region                 string
	cpuOvercommitFactor    float64
	cpuOvercommitEnforce   bool
	placementCPUAware      bool
	publicHostname         string
	publicAliases          []string
	publicBaseDomains      []string
	publicPort             int

	proxyProtocol        bool
	proxyProtocolTrusted []string
	clientIPHeaders      []string
	trustedProxyCIDRs    []string

	otelDropLabels []string

	daemonRuntime string
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run Containarium as a dual-protocol daemon service (gRPC + REST)",
	Long: `Start the Containarium daemon with both gRPC and REST/HTTP APIs for container management.

The daemon provides both gRPC and REST APIs for maximum flexibility:
  - gRPC API for high-performance programmatic access (mTLS authentication)
  - REST/HTTP API for web clients and easy integration (Bearer token authentication)
  - Interactive Swagger UI for API exploration and testing
  - OpenAPI spec generation for client code generation

Security:
  - gRPC: Uses mutual TLS (mTLS) for certificate-based authentication
  - REST: Uses Bearer tokens (JWT) for HTTP authentication
  - Both protocols can run simultaneously on different ports

Examples:
  # Run with both gRPC and REST APIs
  containarium daemon --mtls --rest --jwt-secret your-secret-key

  # Run gRPC only (original behavior)
  containarium daemon --mtls --rest=false

  # Run REST only (development)
  containarium daemon --rest --jwt-secret dev-secret

  # Use JWT secret from file
  containarium daemon --mtls --rest --jwt-secret-file /etc/containarium/jwt.secret

  # Custom ports
  containarium daemon --port 50051 --http-port 8080 --rest --jwt-secret secret

  # Run as systemd service (recommended for production)
  sudo systemctl start containarium`,
	RunE: runDaemon,
}

// poolHeadroomFloors is --pool-headroom-floor, filled and validated by pflag
// (#3128).
var poolHeadroomFloors = poolcapacity.Floors{}

func init() {
	rootCmd.AddCommand(daemonCmd)

	// gRPC settings
	daemonCmd.Flags().StringVar(&daemonAddress, "address", "0.0.0.0", "Address to listen on")
	daemonCmd.Flags().StringVar(&daemonGuardrailStagingRoot, "guardrail-staging-root", "",
		"Absolute directory a guardrail-gated recipe deploy reads its staged dataset from, as <root>/<caller subject>/<staging_ref>. Empty = gated deploys are refused")
	daemonCmd.Flags().StringVar(&daemonGuardrailSnapshotDir, "guardrail-snapshot-dir", os.Getenv("CONTAINARIUM_GUARDRAIL_SNAPSHOT_DIR"),
		"Daemon-private directory, outside the staging root, for the gate's verification snapshots (a full copy of each staged dataset while a deploy runs; point it at provisioned storage, not tmpfs). Empty = the OS temp dir (env: CONTAINARIUM_GUARDRAIL_SNAPSHOT_DIR)")
	daemonCmd.Flags().IntVar(&daemonPort, "port", 50051, "gRPC port to listen on")
	daemonCmd.Flags().BoolVar(&enableMTLS, "mtls", false, "Serve the external gRPC listener, requiring mutual TLS (identity comes from the client certificate). Without it there is no external gRPC listener; the REST gateway uses an in-process one")
	daemonCmd.Flags().StringVar(&daemonCertsDir, "certs-dir", mtls.DefaultCertsDir, "Directory containing TLS certificates")

	// HTTP/REST settings
	daemonCmd.Flags().IntVar(&daemonHTTPPort, "http-port", 8080, "HTTP/REST port to listen on")
	daemonCmd.Flags().DurationVar(&runJournalRetention, "run-journal-retention", server.DefaultRunJournalRetention,
		"How long a run's journal (/var/log/agent-runtime/runs/<run_id>) stays on an agent box before the daemon's sweep removes it")
	daemonCmd.Flags().DurationVar(&runCheckoutMaxAge, "run-checkout-max-age", server.DefaultRunCheckoutMaxAge,
		"How old a platform run's per-run checkout (~/work/<repo>-run-<8 hex> of a box user) must be, by its newest modification, before the daemon's hourly sweep removes it. 0 turns the sweep off (#3296)")
	daemonCmd.Flags().BoolVar(&enableREST, "rest", true, "Enable HTTP/REST API gateway")
	daemonCmd.Flags().DurationVar(&sshAuthzMaxKeyAge, "ssh-authz-max-key-age", envDuration("CONTAINARIUM_SSH_AUTHZ_MAX_KEY_AGE", 0),
		"Self-host SSH authorization: refuse a key registered at least this long ago (KEY_EXPIRED). 0 (default) = no limit. Applies only on a daemon with no cloud enrollment. Env: CONTAINARIUM_SSH_AUTHZ_MAX_KEY_AGE (#2517).")
	daemonCmd.Flags().BoolVar(&sshAuthzKeysCommand, "ssh-authz-keys-command", envBool("CONTAINARIUM_SSH_AUTHZ_KEYS_COMMAND", false),
		"Let the jump host's sshd AuthorizedKeysCommand (containarium ssh-authz-keys) ask this daemon about direct logins: /v1/ssh/authorize also accepts a JWT carrying the ssh:authorize scope, alongside the sentinel signature. Off by default (sentinel-only). Env: CONTAINARIUM_SSH_AUTHZ_KEYS_COMMAND (#2527).")

	// Authentication settings
	daemonCmd.Flags().StringVar(&jwtSecret, "jwt-secret", "", "JWT secret key for REST API authentication")
	daemonCmd.Flags().StringVar(&jwtSecretFile, "jwt-secret-file", "", "Path to file containing JWT secret key")

	// Swagger/OpenAPI settings
	daemonCmd.Flags().StringVar(&swaggerDir, "swagger-dir", "api/swagger", "Directory containing Swagger/OpenAPI files")

	// Infrastructure settings
	daemonCmd.Flags().StringVar(&networkSubnet, "network-subnet", "10.100.0.1/24", "IPv4 subnet for container network (CIDR format, e.g., 10.100.0.1/24)")
	daemonCmd.Flags().BoolVar(&skipInfraInit, "skip-infra-init", false, "Skip automatic infrastructure initialization (storage, network, profile)")
	daemonCmd.Flags().BoolVar(&requireIsolatedStorage, "require-isolated-storage", false, "Refuse to start on a storage pool that does not give each container its own volume. The dir driver puts every tenant rootfs on one filesystem, so they share one journal and one tenant's writeback can stall another tenant's fsync (see #1206). Off by default: a shared journal is harmless on a dev host or a single-tenant box. Turn it on for backends running mutually untrusting tenants.")
	daemonCmd.Flags().BoolVar(&standaloneMode, "standalone", false, "Standalone mode: skip core containers (PostgreSQL, Caddy) and start immediately")
	daemonCmd.Flags().BoolVar(&disableSecurityScanner, "disable-security-scanner", false, "Disable the scan job queue and report store that otherwise start whenever Postgres is configured. The daemon no longer starts any scan on its own (#2631): scans run only when a box owner requests one. Off by default; set true to skip the queue entirely.")
	daemonCmd.Flags().BoolVar(&disablePentestScanner, "disable-pentest-scanner", false, "Disable the network pentest scanner (headers/TLS/ports/web/DNS) that otherwise starts automatically whenever Postgres is configured. Same rationale as --disable-security-scanner; independent flag since an operator may want one scanner without the other.")
	daemonCmd.Flags().BoolVar(&disableZapScanner, "disable-zap-scanner", false, "Disable the ZAP web-app scanner that otherwise starts automatically whenever Postgres is configured. Same rationale as --disable-security-scanner.")

	// App hosting settings
	daemonCmd.Flags().BoolVar(&enableAppHosting, "app-hosting", false, "Enable app hosting feature (requires PostgreSQL)")
	daemonCmd.Flags().StringVar(&postgresConnString, "postgres", "", "PostgreSQL connection string for app hosting (e.g., postgres://user:pass@host:5432/db)")
	daemonCmd.Flags().StringVar(&baseDomain, "base-domain", "example.org", "Base domain for app subdomains (e.g., example.org)")
	daemonCmd.Flags().IntVar(&anonMaxBoxes, "anon-max-boxes", 0, "Anonymous-box door: cap on live unclaimed boxes on this daemon (0 = default 20)")
	daemonCmd.Flags().IntVar(&anonKeyCreatesPer10, "anon-key-creates-per-10min", 0, "Anonymous-box door: box creates allowed per key per 10 minutes (0 = default 1)")
	daemonCmd.Flags().IntVar(&anonKeyBurst, "anon-key-burst", 0, "Anonymous-box door: creation burst per key (0 = default 2)")
	daemonCmd.Flags().IntVar(&anonIPCreatesPer10, "anon-ip-creates-per-10min", 0, "Anonymous-box door: box creates allowed per source IP per 10 minutes (0 = default 6)")
	daemonCmd.Flags().IntVar(&anonIPBurst, "anon-ip-burst", 0, "Anonymous-box door: creation burst per source IP (0 = default 6)")
	daemonCmd.Flags().StringVar(&anonDoorStatePath, "anon-door-state", "", "Anonymous-box door: file persisting the kill switch and bans (default /var/lib/containarium/anon-door.json; a malformed file closes the door)")
	daemonCmd.Flags().StringVar(&anonReminderWebhook, "anon-reminder-webhook", "", "Anonymous-box door only: URL that receives one POST {email, box_name, claim_url, expires_at} per opt-in expiry reminder (`containarium remind-me`); the control plane turns it into an email. Empty = reminders never sent (#2206).")
	daemonCmd.Flags().StringVar(&anonClaimURLBase, "anon-claim-url-base", "", "Anonymous-box door only (CONTAINARIUM_ANON_DOOR=enable): URL the guest's claim-url file points at, e.g. https://<cloud-domain>/claim — the token is appended as ?token=…. Empty = the bare token is written and `containarium claim` prints it with a note (#2199).")
	daemonCmd.Flags().StringVar(&caddyAdminURL, "caddy-admin-url", "", "Caddy admin API URL for reverse proxy configuration (leave empty for auto-setup with --app-hosting)")
	daemonCmd.Flags().StringVar(&caddyCertDir, "caddy-cert-dir", "/var/lib/caddy/.local/share/caddy", "Caddy certificate directory (for sentinel cert sync via /certs endpoint)")

	// Alerting settings
	daemonCmd.Flags().StringVar(&alertWebhookURL, "alert-webhook-url", "", "Webhook URL for alert notifications (optional)")

	// Multi-backend peer settings
	daemonCmd.Flags().StringVar(&sentinelURL, "sentinel-url", "", "Sentinel URL for auto-discovering tunnel peers, event-driven SSH key resync and self-upgrade (e.g., http://10.128.0.5:8081). Falls back to CONTAINARIUM_SENTINEL_URL when unset.")
	daemonCmd.Flags().StringVar(&sshHost, "ssh-host", "", "Public SSH host clients dial to reach containers (the sentinel's SSH endpoint, e.g. region-a.example.com). Surfaced on each Container.ssh_host so clients build the target username@ssh_host. Empty = direct mode: ssh_host is left empty and clients use the container IP.")
	daemonCmd.Flags().StringSliceVar(&peerAddrs, "peers", nil, "Static peer daemon addresses as host:port of each peer's own daemon (e.g., 10.0.0.12:8080). Tunnel backends behind a sentinel are found with --sentinel-url instead.")
	daemonCmd.Flags().StringVar(&localBackendID, "backend-id", "", "This daemon's backend ID (defaults to hostname)")
	daemonCmd.Flags().StringVar(&pool, "pool", "", "Pool name to scope sentinel peer discovery (empty = unscoped, see all peers)")
	daemonCmd.Flags().StringVar(&storagePool, "storage-pool", "", "Incus STORAGE pool containers are created on (default \"default\"). Unrelated to --pool, which names a sentinel-fronted cluster. Point this at a second, per-container-volume pool to migrate tenants off a shared-filesystem `dir` pool without every newly created tenant landing back on it (#1206, #1213).")
	daemonCmd.Flags().StringVar(&zfsTenantRoot, "zfs-tenant-root", "", "ZFS dataset each tenant's encryptionroot is created under for per-tenant encryption (#1199), e.g. \"incus-local/tenants\". Empty derives it from the storage pool's source. Has no effect until key custody is configured — an encrypted create is refused without a KeyProvider.")
	daemonCmd.Flags().StringVar(&zfsKeysDir, "zfs-keys-dir", "", "Directory holding per-tenant ZFS encryption keys, e.g. \"/etc/containarium/keys\". Setting it configures file-based key custody and makes `containarium create --encrypted` work; leaving it unset (the default) means an encrypted create is refused rather than silently producing an unencrypted container. Keys are generated per tenant on first use and never leave this directory.")
	daemonCmd.Flags().StringVar(&region, "region", "", "Region this backend serves; recorded in its capability profile (containarium backends profile). Empty falls back to the --pool name.")
	daemonCmd.Flags().StringVar(&publicHostname, "public-hostname", "", "Public hostname this primary serves (e.g. prod.example.com); enables sentinel primary registration")
	daemonCmd.Flags().StringSliceVar(&publicAliases, "public-aliases", nil, "Additional hostnames the primary's Caddy serves (e.g. api.example.com,voice.example.com); the sentinel SNI router treats these as aliases of --public-hostname")
	daemonCmd.Flags().StringSliceVar(&publicBaseDomains, "public-base-domain", nil, "Suffix-match anchor advertised to the sentinel — inbound SNI of the form <anything>.<public-base-domain> routes here without each subdomain being a registered alias. Repeatable: list multiple to host workloads under different parent domains on the same backend (e.g. --public-base-domain lab.example.com --public-base-domain demo.example.org). Defaults to [--base-domain] when unset. See docs/PER-POOL-BASE-DOMAIN.md.")
	daemonCmd.Flags().BoolVar(&proxyProtocol, "proxy-protocol", false, "Configure Caddy to accept PROXY v2 headers from --proxy-protocol-trusted CIDRs so containers receive the real client IP. Pair with --proxy-protocol on the sentinel.")
	daemonCmd.Flags().StringSliceVar(&proxyProtocolTrusted, "proxy-protocol-trusted", []string{"127.0.0.0/8"}, "CIDRs allowed to send PROXY headers (typically the sentinel VPC IP/32). Wildcard 0.0.0.0/0 is rejected.")
	daemonCmd.Flags().StringSliceVar(&clientIPHeaders, "client-ip-header", nil, "Header(s) Caddy derives the original client IP from when this host sits behind a CDN/reverse proxy that terminates the client connection (e.g. Cf-Connecting-Ip for Cloudflare). Honored only from --trusted-proxy-cidrs and --proxy-protocol-trusted peers. Repeatable. Empty = off.")
	daemonCmd.Flags().StringSliceVar(&trustedProxyCIDRs, "trusted-proxy-cidrs", nil, "Extra CIDRs unioned into Caddy's trusted_proxies — typically the CDN's published ranges — so --client-ip-header is trusted only from them. Does NOT widen the PROXY-protocol allow list. Wildcard 0.0.0.0/0 is rejected.")
	daemonCmd.Flags().IntVar(&publicPort, "public-port", 443, "Public TLS port the sentinel forwards to (default 443)")

	// OTel collector settings
	daemonCmd.Flags().StringSliceVar(&otelDropLabels, "otel-drop-labels", nil, "Extra attribute keys (comma-separated) the app-side OTel collector drops on top of the built-in PII/cardinality defaults (request_id, trace_id, user_email, session_id, correlation_id).")

	// Runtime selection
	daemonCmd.Flags().StringVar(&daemonRuntime, "runtime", "", `Box backend: "lxc" (default) or "k8s". Falls back to CONTAINARIUM_RUNTIME env when unset.`)
	daemonCmd.Flags().Var(poolHeadroomFloors, "pool-headroom-floor", "Refuse container creates and snapshots on an Incus storage pool once it is this percent used, as <pool>=<percent>. Repeatable, one pool each; whole percent 86-99 (above the 85% \"pool nearly full\" mark). Pools not named use 95. Host status and the built-in alert keep 85/95 (#3128).")
	daemonCmd.Flags().Float64Var(&cpuOvercommitFactor, "cpu-overcommit-factor", envFloat("CONTAINARIUM_CPU_OVERCOMMIT_FACTOR", 0), "Max CPU overcommit: refuse a create when committed cores would exceed logical-CPUs (vCPUs, incl. SMT threads) × this factor. 0 (default) disables the check. Env: CONTAINARIUM_CPU_OVERCOMMIT_FACTOR (#1029).")
	daemonCmd.Flags().BoolVar(&cpuOvercommitEnforce, "cpu-overcommit-enforce", envBool("CONTAINARIUM_CPU_OVERCOMMIT_ENFORCE", false), "With --cpu-overcommit-factor > 0, actually reject over-ceiling creates. When false (default), the check is advisory (logs what it would reject). Env: CONTAINARIUM_CPU_OVERCOMMIT_ENFORCE (#1029).")
	daemonCmd.Flags().BoolVar(&placementCPUAware, "placement-cpu-aware", envBool("CONTAINARIUM_PLACEMENT_CPU_AWARE", false), "When a pool create has no explicit backend, place it on the least CPU-committed healthy peer instead of an arbitrary one. Off by default (first-healthy). Env: CONTAINARIUM_PLACEMENT_CPU_AWARE (#1029).")
}

// envFloat reads name as a float64, returning def when unset or unparseable.
func envFloat(name string, def float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// envDuration reads name as a time.Duration, returning def when unset or
// unparseable.
func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

