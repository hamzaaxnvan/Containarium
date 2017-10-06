package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/releases"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/stacks"
	"github.com/footprintai/containarium/pkg/version"
)

const (
	// CorePostgresContainer is the name of the core PostgreSQL container
	CorePostgresContainer = "containarium-core-postgres"

	// CoreCaddyContainer is the name of the core Caddy container
	CoreCaddyContainer = "containarium-core-caddy"

	// Default PostgreSQL credentials
	DefaultPostgresUser     = "containarium"
	DefaultPostgresPassword = "containarium"
	DefaultPostgresDB       = "containarium"
	DefaultPostgresPort     = 5432

	// Default Caddy admin port
	DefaultCaddyAdminPort = 2019

	// CoreSecurityContainer is the name of the core ClamAV security container
	CoreSecurityContainer = "containarium-core-security"

	// CoreGuacamoleContainer is the name of the core Guacamole container
	CoreGuacamoleContainer = "containarium-core-guacamole"

	// CoreVictoriaMetricsContainer is the name of the core Victoria Metrics + Grafana container
	CoreVictoriaMetricsContainer = "containarium-core-victoriametrics"

	// Default Victoria Metrics port
	DefaultVMPort = 8428

	// Default Grafana port
	DefaultGrafanaPort = 3000

	// Default Victoria Metrics retention period
	DefaultVMRetention = "30d"

	// Default vmalert port
	DefaultVMAlertPort = 8880

	// Default Alertmanager port
	DefaultAlertmanagerPort = 9093
)

// coreBridgeName is the incus managed bridge the core + tenant containers
// attach to. Matches the value hard-coded at the GetNetworkSubnet call sites.
const coreBridgeName = "incusbr0"

// coreStaticIPHostOffsets pins each core container to a stable host number high
// in the bridge subnet (well clear of the low addresses incus' DHCP hands out
// first). incus reserves a NIC's ipv4.address and excludes it from the DHCP
// pool, so these never collide with a tenant lease.
//
// Why (#240): core-caddy's IP is hard-coded into the split-horizon dnsmasq
// record (and historically a host DNAT rule) with NO reconciler. Its DHCP lease
// changes on recreate, stranding those references — internal clients then
// resolve every platform hostname under the base domain to a dead IP. A
// deterministic IP that survives a daemon-driven recreate keeps those
// references self-consistent. Only caddy is pinned today (it's the one those
// references point at); the map is keyed by container name so others can opt in.
//
// Postgres and the metrics container opt in so that the pg_hba.conf written when
// Postgres is first installed can name the one address Grafana will connect from,
// instead of admitting the whole subnet. As with caddy this applies when the
// container is created; an existing container keeps the address it has.
var coreStaticIPHostOffsets = map[string]uint32{
	CoreCaddyContainer:           241,
	CorePostgresContainer:        242,
	CoreVictoriaMetricsContainer: 243,
	CoreOTelCollectorContainer:   244,
	CoreSecurityContainer:        245,
	CoreGuacamoleContainer:       246,
}

// coreStaticIP returns the deterministic static IP for a core container within
// networkCIDR (gateway or network form, e.g. "10.100.0.1/24" — ParseCIDR masks
// either to the network base). Returns ("", nil) when the container has no
// assigned offset (caller falls back to DHCP); an error only when the CIDR is
// unparseable, not IPv4, or too small to hold the offset.
func coreStaticIP(networkCIDR, containerName string) (string, error) {
	offset, ok := coreStaticIPHostOffsets[containerName]
	if !ok {
		return "", nil
	}
	_, ipnet, err := net.ParseCIDR(networkCIDR)
	if err != nil {
		return "", fmt.Errorf("parse network cidr %q: %w", networkCIDR, err)
	}
	base := ipnet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("network %q is not IPv4", networkCIDR)
	}
	ones, bits := ipnet.Mask.Size()
	hostCount := uint32(1) << uint(bits-ones)
	// Reject the network (.0) and broadcast (last) addresses and anything past
	// the subnet — a misconfigured tiny subnet shouldn't yield a bogus IP.
	if offset == 0 || offset >= hostCount-1 {
		return "", fmt.Errorf("host offset %d out of range for %q", offset, networkCIDR)
	}
	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, binary.BigEndian.Uint32(base)+offset)
	return out.String(), nil
}

// CoreServicesConfig holds configuration for core services
type CoreServicesConfig struct {
	// PostgreSQL settings
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string

	// Network settings
	NetworkCIDR string // e.g., "10.100.0.0/24"

	// Alerting settings
	AlertWebhookURL string // Webhook URL for alert notifications (optional)
}

// CoreServices manages the core infrastructure containers (PostgreSQL, Caddy, VictoriaMetrics+Grafana)
type CoreServices struct {
	incusClient       incus.Backend
	config            CoreServicesConfig
	postgresIP        string
	caddyIP           string
	victoriaMetricsIP string
	otelCollectorIP   string

	// pgPing opens one real connection with the given credentials. It is a
	// field only so tests can script the result; production uses pgxPing.
	pgPing func(context.Context, pgPingArgs) error

	// pgLoginProbe logs in to Postgres the way the daemon will; nil means the
	// real network login. A field only so tests can script the result.
	pgLoginProbe func(context.Context) error
}

// NewCoreServices creates a new core services manager
func NewCoreServices(incusClient incus.Backend, config CoreServicesConfig) *CoreServices {
	if config.PostgresUser == "" {
		config.PostgresUser = DefaultPostgresUser
	}
	if config.PostgresPassword == "" {
		// Resolve the password the same way the daemon's own connection does
		// (secret file, then env, then the dev default). No caller sets this
		// field, so without this the first-install CREATE USER, the readiness
		// probe and Grafana's config all used the compiled-in default even on
		// a host configured with its own password (#2091). Quiet: this runs
		// several times per start and the daemon already logs the default
		// warning once.
		pw, _, err := resolvePostgresPasswordQuiet()
		if err != nil {
			log.Printf("ERROR: %v — falling back to the compiled-in default for core services", err)
			pw = DefaultPostgresPassword
		}
		config.PostgresPassword = pw
	}
	if config.PostgresDB == "" {
		config.PostgresDB = DefaultPostgresDB
	}
	if config.NetworkCIDR == "" {
		config.NetworkCIDR = "10.100.0.0/24"
	}

	return &CoreServices{
		incusClient: incusClient,
		config:      config,
		pgPing:      pgxPing,
	}
}

// pinCoreIP gives a container being created the deterministic address it has in
// coreStaticIPHostOffsets, so a recreate reuses it. Best effort: when the
// address cannot be computed it logs and leaves the container on DHCP, which is
// what happened before containers other than caddy were pinned.
func (cs *CoreServices) pinCoreIP(config *incus.ContainerConfig, name string) {
	staticIP, err := coreStaticIP(cs.config.NetworkCIDR, name)
	if err != nil {
		log.Printf("Warning: could not compute stable IP for %s (%v); using DHCP", name, err)
		return
	}
	if staticIP == "" {
		return
	}
	config.NIC = &incus.NICDevice{Name: "eth0", Network: coreBridgeName, IPv4Address: staticIP}
	log.Printf("Assigning stable IP %s to %s", staticIP, name)
}

// ensureCoreReservation sets a ZFS reservation on a core container's dataset so
// it can always write even when user containers fill the pool. This prevents
// cascade failures (e.g. PostgreSQL crashing when the pool is 100% full
// because user containers overflowed their quotas).
//
// Idempotent — safe to call repeatedly. Silently skips on non-ZFS pools.
func (cs *CoreServices) ensureCoreReservation(containerName, size string) {
	dataset := fmt.Sprintf("incus-pool/containers/containers/%s", containerName)
	cmd := exec.Command("zfs", "set", "reservation="+size, dataset)
	if out, err := cmd.CombinedOutput(); err != nil {
		// ZFS may not be present (dir-backed pool) — not fatal
		log.Printf("Note: skipping ZFS reservation for %s: %v (output: %s)", containerName, err, string(out))
	}
}

// ensurePostgresRestartPolicy adds a systemd override so postgresql@16-main
// auto-restarts on failure. Ubuntu 24.04 runs postgres via postgresql@16-main.service
// (a template unit), not plain postgresql.service. Idempotent — safe to call repeatedly.
func (cs *CoreServices) ensurePostgresRestartPolicy() {
	if err := cs.incusClient.Exec(CorePostgresContainer, []string{
		"bash", "-c",
		"mkdir -p /etc/systemd/system/postgresql@.service.d && " +
			"printf '[Service]\\nRestart=on-failure\\nRestartSec=5s\\n' > /etc/systemd/system/postgresql@.service.d/restart.conf && " +
			"systemctl daemon-reload",
	}); err != nil {
		log.Printf("Warning: failed to set postgresql restart policy: %v", err)
	}
}

// EnsurePostgres ensures PostgreSQL container is running and returns the connection string
func (cs *CoreServices) EnsurePostgres(ctx context.Context) (string, error) {
	// Reserve 5GB so postgres can always write (prevents cascade failures)
	cs.ensureCoreReservation(CorePostgresContainer, "5G")

	// Check if container already exists
	info, err := cs.incusClient.GetContainer(CorePostgresContainer)
	if err == nil {
		// Backfill role label and boot priority for containers created before this change
		cs.backfillConfig(CorePostgresContainer, incus.RolePostgres, "100")

		// Container exists
		if info.State == "Running" {
			cs.postgresIP = info.IPAddress
			log.Printf("PostgreSQL container already running at %s", cs.postgresIP)
			// Always re-apply postgresql auto-restart override (idempotent)
			cs.ensurePostgresRestartPolicy()
			return cs.getPostgresConnString(), nil
		}
		// Container exists but not running, start it
		log.Printf("Starting existing PostgreSQL container...")
		if err := cs.incusClient.StartContainer(CorePostgresContainer); err != nil {
			return "", fmt.Errorf("failed to start postgres container: %w", err)
		}
		// Wait for network
		ip, err := cs.incusClient.WaitForNetwork(CorePostgresContainer, 60*time.Second)
		if err != nil {
			return "", fmt.Errorf("failed to get postgres IP: %w", err)
		}
		cs.postgresIP = ip
		// Wait for PostgreSQL to be ready
		if err := cs.waitForPostgres(ctx); err != nil {
			return "", err
		}
		return cs.getPostgresConnString(), nil
	}

	// Container doesn't exist, create it
	log.Printf("Creating PostgreSQL container...")

	config := incus.ContainerConfig{
		Name:      CorePostgresContainer,
		Image:     "images:ubuntu/24.04",
		CPU:       "2",
		Memory:    "2GB",
		AutoStart: true,
		Disk: &incus.DiskDevice{
			Path: "/",
			Pool: cs.incusClient.StoragePool(),
			Size: "10GB",
		},
	}

	cs.pinCoreIP(&config, CorePostgresContainer)

	if err := cs.incusClient.CreateContainer(config); err != nil {
		return "", fmt.Errorf("failed to create postgres container: %w", err)
	}

	// Set role label and boot priority
	cs.setContainerConfigBestEffort(CorePostgresContainer, incus.RoleKey, string(incus.RolePostgres))
	cs.setContainerConfigBestEffort(CorePostgresContainer, "boot.autostart", "true")
	cs.setContainerConfigBestEffort(CorePostgresContainer, "boot.autostart.priority", "100")

	// Start container
	if err := cs.incusClient.StartContainer(CorePostgresContainer); err != nil {
		return "", fmt.Errorf("failed to start postgres container: %w", err)
	}

	// Wait for network
	ip, err := cs.incusClient.WaitForNetwork(CorePostgresContainer, 60*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to get postgres IP: %w", err)
	}
	cs.postgresIP = ip
	log.Printf("PostgreSQL container IP: %s", cs.postgresIP)

	// Install and configure PostgreSQL
	if err := cs.setupPostgres(ctx); err != nil {
		return "", fmt.Errorf("failed to setup postgres: %w", err)
	}

	// Apply base scripts (timezone, clamav) — best-effort
	cs.applyBaseScripts(CorePostgresContainer, "ubuntu")

	return cs.getPostgresConnString(), nil
}

// setupPostgres installs and configures PostgreSQL in the container
func (cs *CoreServices) setupPostgres(ctx context.Context) error {
	log.Printf("Installing PostgreSQL...")

	// Wait for apt to be available
	time.Sleep(5 * time.Second)

	// Update and install PostgreSQL
	commands := [][]string{
		{"apt-get", "update"},
		{"apt-get", "install", "-y", "postgresql", "postgresql-contrib"},
	}

	for _, cmd := range commands {
		if err := cs.incusClient.Exec(CorePostgresContainer, cmd); err != nil {
			return fmt.Errorf("failed to run %v: %w", cmd, err)
		}
	}

	// Add auto-restart policy for PostgreSQL (idempotent)
	cs.ensurePostgresRestartPolicy()

	// Start PostgreSQL
	if err := cs.incusClient.Exec(CorePostgresContainer, []string{"systemctl", "start", "postgresql"}); err != nil {
		return fmt.Errorf("failed to start postgresql: %w", err)
	}

	if err := cs.incusClient.Exec(CorePostgresContainer, []string{"systemctl", "enable", "postgresql"}); err != nil {
		return fmt.Errorf("failed to enable postgresql: %w", err)
	}

	// Create user and database
	createUserCmd := fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s';", cs.config.PostgresUser, cs.config.PostgresPassword)
	createDBCmd := fmt.Sprintf("CREATE DATABASE %s OWNER %s;", cs.config.PostgresDB, cs.config.PostgresUser)

	if err := cs.incusClient.Exec(CorePostgresContainer, []string{
		"su", "-", "postgres", "-c", fmt.Sprintf("psql -c \"%s\"", createUserCmd),
	}); err != nil {
		// User might already exist, continue
		log.Printf("Note: PostgreSQL user might already exist")
	}

	if err := cs.incusClient.Exec(CorePostgresContainer, []string{
		"su", "-", "postgres", "-c", fmt.Sprintf("psql -c \"%s\"", createDBCmd),
	}); err != nil {
		// Database might already exist, continue
		log.Printf("Note: PostgreSQL database might already exist")
	}

	// Configure PostgreSQL to listen on all interfaces
	if err := cs.incusClient.Exec(CorePostgresContainer, []string{
		"bash", "-c", fmt.Sprintf("sed -i \"s/#listen_addresses = 'localhost'/listen_addresses = '*'/\" %s", pgConfPath),
	}); err != nil {
		return fmt.Errorf("failed to update postgresql.conf: %w", err)
	}

	// Limit who may log in to the core clients, turn on connection logging,
	// restart, and prove the daemon can still log in.
	if err := cs.configurePostgresAccess(ctx); err != nil {
		return err
	}

	log.Printf("PostgreSQL setup complete")
	return nil
}

// waitForPostgres waits for PostgreSQL to be ready to accept connections
func (cs *CoreServices) waitForPostgres(ctx context.Context) error {
	log.Printf("Waiting for PostgreSQL to be ready...")

	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to connect
		if err := cs.incusClient.Exec(CorePostgresContainer, []string{
			"su", "-", "postgres", "-c", "pg_isready",
		}); err == nil {
			log.Printf("PostgreSQL is ready")
			return nil
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout waiting for PostgreSQL to be ready")
}

// getPostgresConnString returns the PostgreSQL connection string
func (cs *CoreServices) getPostgresConnString() string {
	return PostgresDSN(cs.config.PostgresUser, cs.config.PostgresPassword,
		cs.postgresIP, DefaultPostgresPort, cs.config.PostgresDB)
}

// GetPostgresIP returns the PostgreSQL container IP
func (cs *CoreServices) GetPostgresIP() string {
	return cs.postgresIP
}

// EnsureCaddy ensures Caddy container is running and returns the admin URL
func (cs *CoreServices) EnsureCaddy(ctx context.Context, baseDomain string) (string, error) {
	// Reserve 2GB so Caddy can always write (prevents cascade failures)
	cs.ensureCoreReservation(CoreCaddyContainer, "2G")

	// Check if container already exists
	info, err := cs.incusClient.GetContainer(CoreCaddyContainer)
	if err == nil {
		// Backfill role label and boot priority for containers created before this change
		cs.backfillConfig(CoreCaddyContainer, incus.RoleCaddy, "90")

		// Container exists
		if info.State == "Running" {
			cs.caddyIP = info.IPAddress
			log.Printf("Caddy container already running at %s", cs.caddyIP)
			if err := cs.ensureCaddyModules(ctx, baseDomain); err != nil {
				return "", err
			}
			cs.reconcileCaddyDNSEnv()
			return cs.getCaddyAdminURL(), nil
		}
		// Container exists but not running, start it
		log.Printf("Starting existing Caddy container...")
		if err := cs.incusClient.StartContainer(CoreCaddyContainer); err != nil {
			return "", fmt.Errorf("failed to start caddy container: %w", err)
		}
		ip, err := cs.incusClient.WaitForNetwork(CoreCaddyContainer, 60*time.Second)
		if err != nil {
			return "", fmt.Errorf("failed to get caddy IP: %w", err)
		}
		cs.caddyIP = ip
		if err := cs.ensureCaddyModules(ctx, baseDomain); err != nil {
			return "", err
		}
		cs.reconcileCaddyDNSEnv()
		return cs.getCaddyAdminURL(), nil
	}

	// Container doesn't exist, create it
	log.Printf("Creating Caddy container...")

	config := incus.ContainerConfig{
		Name:      CoreCaddyContainer,
		Image:     "images:ubuntu/24.04",
		CPU:       "1",
		Memory:    "512MB",
		AutoStart: true,
		Disk: &incus.DiskDevice{
			Path: "/",
			Pool: cs.incusClient.StoragePool(),
			Size: "5GB",
		},
	}

	// Pin a stable IP so a recreate reuses it — keeps the split-horizon dnsmasq
	// record / host DNAT that reference caddy's IP valid across recreates (#240).
	// Best-effort: a compute failure logs and falls back to DHCP (today's behaviour).
	if staticIP, err := coreStaticIP(cs.config.NetworkCIDR, CoreCaddyContainer); err != nil {
		log.Printf("Warning: could not compute stable IP for %s (%v); using DHCP", CoreCaddyContainer, err)
	} else if staticIP != "" {
		config.NIC = &incus.NICDevice{Name: "eth0", Network: coreBridgeName, IPv4Address: staticIP}
		log.Printf("Assigning stable IP %s to %s (survives recreate; #240)", staticIP, CoreCaddyContainer)
	}

	if err := cs.incusClient.CreateContainer(config); err != nil {
		return "", fmt.Errorf("failed to create caddy container: %w", err)
	}

	// Set role label and boot priority
	cs.setContainerConfigBestEffort(CoreCaddyContainer, incus.RoleKey, string(incus.RoleCaddy))
	cs.setContainerConfigBestEffort(CoreCaddyContainer, "boot.autostart", "true")
	cs.setContainerConfigBestEffort(CoreCaddyContainer, "boot.autostart.priority", "90")

	// Start container
	if err := cs.incusClient.StartContainer(CoreCaddyContainer); err != nil {
		return "", fmt.Errorf("failed to start caddy container: %w", err)
	}

	// Wait for network
	ip, err := cs.incusClient.WaitForNetwork(CoreCaddyContainer, 60*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to get caddy IP: %w", err)
	}
	cs.caddyIP = ip
	log.Printf("Caddy container IP: %s", cs.caddyIP)

	// Install and configure Caddy
	if err := cs.setupCaddy(ctx, baseDomain); err != nil {
		return "", fmt.Errorf("failed to setup caddy: %w", err)
	}

	// Apply base scripts (timezone, clamav) — best-effort
	cs.applyBaseScripts(CoreCaddyContainer, "ubuntu")

	return cs.getCaddyAdminURL(), nil
}

// caddyReleaseBinaryName is the release asset setupCaddy tries first — one
// Caddy binary with caddy-l4 + every supported DNS-01 provider baked in,
// published by `make build-caddy` alongside the daemon's own release
// artifacts (#1617).
const caddyReleaseBinaryName = "caddy-linux-amd64"

// setupCaddy installs and configures Caddy in the container. It first tries
// downloading + checksum-verifying the release-published binary matching
// this daemon's own version (#1617) — no compiler, no proxy.golang.org
// egress, no per-host drift, and every DNS-01 provider is already baked in
// so switching providers later is a config change, not a rebuild. Falls
// back to building from source with xcaddy (the pre-#1617 behavior) when no
// matching release asset exists yet — an older daemon build, or a release
// that predates this asset — or the download/verify fails for any reason.
func (cs *CoreServices) setupCaddy(ctx context.Context, baseDomain string) error {
	log.Printf("Installing Caddy...")

	// Wait for apt to be available
	time.Sleep(5 * time.Second)

	if err := cs.installPrebuiltCaddy(ctx); err != nil {
		log.Printf("Prebuilt Caddy install unavailable (%v) — falling back to an xcaddy source build", err)
		if err := cs.buildCaddyFromSource(); err != nil {
			return err
		}
	}

	// Assert the binary actually has the module the daemon's own DNS-01
	// config depends on. Cheap, and catches a wrong-build (this daemon's own
	// stale binary, a fallback source build with the module map lookup
	// somehow missing an entry) at provisioning time instead of as a
	// reconcile-loop 500 days later — exactly Containarium-cloud#1351's
	// shape, which this check would have caught immediately (#1617).
	if err := cs.verifyCaddyModules(); err != nil {
		return err
	}

	// Create caddy user and directories (idempotent; log on failure)
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{
		"bash", "-c", "id caddy >/dev/null 2>&1 || useradd --system --home /var/lib/caddy --shell /usr/sbin/nologin caddy",
	}); err != nil {
		log.Printf("Warning: failed to ensure caddy user: %v", err)
	}
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"mkdir", "-p", "/var/lib/caddy", "/etc/caddy"}); err != nil {
		log.Printf("Warning: failed to create caddy directories: %v", err)
	}
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"chown", "-R", "caddy:caddy", "/var/lib/caddy"}); err != nil {
		log.Printf("Warning: failed to chown caddy directory: %v", err)
	}

	// Create Caddyfile with admin API enabled
	caddyfile := `{
	admin :2019
}

# Default catch-all - will be configured dynamically via admin API
`

	if err := cs.incusClient.WriteFile(CoreCaddyContainer, "/etc/caddy/Caddyfile", []byte(caddyfile), "0644"); err != nil {
		return fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	// Create systemd service for Caddy. Injects one Environment= line per
	// DNS-01 provider credential Caddy needs to expand from ITS OWN process
	// environment — see caddyServiceUnit (#1597).
	present := resolveDNSProviderCredential()
	systemdUnit := caddyServiceUnit(present)
	if err := cs.incusClient.WriteFile(CoreCaddyContainer, "/etc/systemd/system/caddy.service", []byte(systemdUnit), "0644"); err != nil {
		return fmt.Errorf("failed to write caddy service: %w", err)
	}

	// Enable and start Caddy
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"systemctl", "daemon-reload"}); err != nil {
		log.Printf("Warning: systemctl daemon-reload failed: %v", err)
	}
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"systemctl", "enable", "caddy"}); err != nil {
		return fmt.Errorf("failed to enable caddy: %w", err)
	}
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"systemctl", "restart", "caddy"}); err != nil {
		return fmt.Errorf("failed to restart caddy: %w", err)
	}

	// Wait for Caddy to be ready
	time.Sleep(3 * time.Second)

	log.Printf("Caddy setup complete (with caddy-l4 plugin)")
	return nil
}

// installPrebuiltCaddy downloads and checksum-verifies the release-published
// Caddy binary matching this daemon's own version, entirely inside the caddy
// container (#1617). Returns an error — never fatal to the caller, which
// falls back to buildCaddyFromSource — when this daemon's release has no
// matching asset yet, or the download/verify fails for any reason.
func (cs *CoreServices) installPrebuiltCaddy(ctx context.Context) error {
	tag := "v" + version.GetVersion()
	sum, err := releases.FetchSHA256(ctx, tag, caddyReleaseBinaryName)
	if err != nil {
		return fmt.Errorf("fetch SHA256SUMS for %s: %w", tag, err)
	}
	url := releases.BinaryDownloadURL(tag, caddyReleaseBinaryName)
	// #nosec G204 -- url/sum are computed above from a fixed GitHub host and
	// a hex checksum, not from caller-controlled input.
	script := fmt.Sprintf(`set -e
curl -fsSL %q -o /tmp/caddy.new
echo "%s  /tmp/caddy.new" | sha256sum -c -
chmod +x /tmp/caddy.new
mv /tmp/caddy.new /usr/bin/caddy
`, url, sum)
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"bash", "-c", script}); err != nil {
		return fmt.Errorf("download+verify prebuilt caddy from %s: %w", url, err)
	}
	log.Printf("Installed prebuilt Caddy %s (checksum verified, every DNS-01 provider included)", tag)
	return nil
}

// buildCaddyFromSource is setupCaddy's pre-#1617 behavior: install Go +
// xcaddy in the container and compile Caddy with caddy-l4 plus, when an ACME
// DNS-01 provider is configured, the matching caddy-dns module — without it
// Caddy rejects the DNS-01 config the daemon emits (#378). Only the
// configured provider's module is included here (unlike the prebuilt asset,
// which bakes in every provider), matching this path's original behavior;
// switching providers on a host that fell back to a source build still needs
// a rebuild.
func (cs *CoreServices) buildCaddyFromSource() error {
	// Install Go and build dependencies (needed for xcaddy)
	commands := [][]string{
		{"apt-get", "update"},
		{"apt-get", "install", "-y", "curl", "golang-go"},
	}
	for _, cmd := range commands {
		if err := cs.incusClient.Exec(CoreCaddyContainer, cmd); err != nil {
			return fmt.Errorf("failed to run %v: %w", cmd, err)
		}
	}

	// Install xcaddy
	log.Printf("Installing xcaddy...")
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{
		"bash", "-c", "GOBIN=/usr/local/bin go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest",
	}); err != nil {
		return fmt.Errorf("failed to install xcaddy: %w", err)
	}

	// Pin the same Caddy version the prebuilt asset targets (app.CaddyVersion)
	// so a source-build fallback doesn't reintroduce the per-host version
	// drift #1617 removed from the download path.
	buildCmd := "xcaddy build " + app.CaddyVersion + " --with github.com/mholt/caddy-l4"
	if provider := app.DNSProviderFromEnv(); provider != "" {
		if mod := app.DNSProviderModule(provider); mod != "" {
			buildCmd += " --with " + mod
			log.Printf("Building Caddy %s with caddy-l4 + DNS-01 provider %q (%s) (this may take a few minutes)...", app.CaddyVersion, provider, mod)
		} else {
			log.Printf("WARNING: CONTAINARIUM_ACME_DNS_PROVIDER=%q has no known caddy-dns module; building Caddy without it — DNS-01 wildcard issuance will fail until a module is added", provider)
		}
	} else {
		log.Printf("Building Caddy %s with caddy-l4 plugin (this may take a few minutes)...", app.CaddyVersion)
	}
	buildCmd += " --output /usr/bin/caddy"
	if err := cs.incusClient.Exec(CoreCaddyContainer, []string{"bash", "-c", buildCmd}); err != nil {
		return fmt.Errorf("failed to build caddy with xcaddy: %w", err)
	}
	return nil
}

// verifyCaddyModules asserts the installed Caddy binary has the module the
// daemon's own DNS-01 config depends on (#1617). No-op when DNS-01 isn't
// configured, or the configured provider has no known module (already
// warned about during a source build).
func (cs *CoreServices) verifyCaddyModules() error {
	provider := app.DNSProviderFromEnv()
	if provider == "" {
		return nil
	}
	mod := app.DNSProviderModule(provider)
	if mod == "" {
		return nil
	}
	stdout, _, err := cs.incusClient.ExecWithOutput(CoreCaddyContainer, []string{"/usr/bin/caddy", "list-modules"})
	if err != nil {
		return fmt.Errorf("caddy list-modules failed: %w", err)
	}
	wantModuleID := "dns.providers." + provider
	if !strings.Contains(stdout, wantModuleID) {
		return fmt.Errorf("installed Caddy binary is missing module %q (built without %s) — "+
			"DNS-01 wildcard issuance will fail with \"unknown module\" until Caddy is reinstalled with "+
			"this provider included (#1617)", wantModuleID, mod)
	}
	return nil
}

// caddyDNSProviderEnv resolves the {env.VAR} placeholders the daemon's
// configured DNS-01 provider config references (app.EnvPlaceholdersInDNSProvider)
// against the DAEMON's own environment — the source of truth an operator
// actually sets CONTAINARIUM_ACME_DNS_PROVIDER alongside. Returns the ones
// that resolve to a non-empty value (to inject into Caddy's own systemd
// environment, since Caddy runs in a separate container and does not share
// the daemon's process environment) and the ones that don't (#1597: Caddy
// silently expands an unset {env.X} to the empty string, and DNS-01 then
// fails per-issuance with a message that reads like a token-scope problem
// rather than "nothing was ever set").
func caddyDNSProviderEnv() (present map[string]string, missing []string) {
	names := app.EnvPlaceholdersInDNSProvider(app.DNSChallengeFromEnv())
	present = make(map[string]string, len(names))
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			present[name] = v
		} else {
			missing = append(missing, name)
		}
	}
	return present, missing
}

// logMissingDNSProviderCredentials prints one clear, greppable ERROR per
// DNS-01 provider credential the daemon's config references but can't find
// set in its own environment. Deliberately non-fatal — a misconfiguration
// here shouldn't boot-loop the daemon over a transient/self-inflicted
// credential gap — but it must be visible without archaeology through
// Caddy's own ACME logs, which is the whole failure mode #1597 reports:
// five weeks of "configured but not issued, no error is logged".
func logMissingDNSProviderCredentials(missing []string) {
	for _, name := range missing {
		log.Printf("ERROR: CONTAINARIUM_ACME_DNS_PROVIDER's config references {env.%s}, but %s is unset "+
			"(or empty) in the daemon's own environment. Caddy will expand the placeholder to an empty "+
			"string and every DNS-01 challenge will fail — reading, misleadingly, like a token-scope "+
			"problem rather than a missing credential — until %s is set (#1597).", name, name, name)
	}
}

// verifyDNSProviderCredentialFn is a var, not a plain call, so tests that
// exercise reconcileCaddyDNSEnv's propagation/reconcile logic (unrelated to
// verification) can stub it out — without this seam every such test would
// make a real network call to whatever provider API a test token happens to
// name, which is slow, flaky, and points at a live third party.
var verifyDNSProviderCredentialFn = verifyDNSProviderCredential

// verifyDNSProviderCredential authenticates the resolved DNS-01 credential
// against its own provider's API (#1739) — a credential can be present,
// non-empty, and correctly propagated (#1738) and still be revoked,
// expired, or simply wrong. Non-fatal, same as the presence check: an
// ERROR log on rejection, an INFO note when no built-in verifier exists for
// this provider, and — per #1739's own acceptance criterion — nothing at
// all when the credential is confirmed valid.
func verifyDNSProviderCredential(present map[string]string) {
	provider := app.DNSProviderFromEnv()
	if provider == "" || len(present) == 0 {
		return // no DNS-01 configured, or nothing resolved to check (#1738 already reported that)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	attempted, err := app.VerifyDNSProviderCredential(ctx, provider, present)
	if !attempted {
		log.Printf("INFO: no built-in credential verification for DNS-01 provider %q — presence and "+
			"propagation to Caddy were confirmed (#1738), but validity against the provider's own API "+
			"is unchecked (#1739)", provider)
		return
	}
	if err != nil {
		log.Printf("ERROR: DNS-01 provider %q rejected its configured credential: %v — wildcard/DNS-01 "+
			"certificate issuance will fail until this is fixed (#1739)", provider, err)
	}
}

// resolveDNSProviderCredential resolves the DNS-01 provider's {env.VAR}
// placeholders against the daemon's own environment, reports any that are
// missing (#1738), and — for a provider with a built-in verifier — checks
// the resolved credential is actually accepted (#1739). Returns the
// resolved map for the caller to inject into Caddy's systemd environment.
// Shared by setupCaddy (first provisioning) and reconcileCaddyDNSEnv (every
// subsequent daemon start) so both paths get the same checks.
func resolveDNSProviderCredential() map[string]string {
	present, missing := caddyDNSProviderEnv()
	logMissingDNSProviderCredentials(missing)
	verifyDNSProviderCredentialFn(present)
	return present
}

// caddyServiceUnit renders the containarium-core-caddy systemd unit, adding
// one Environment= line per resolved DNS-01 provider credential so Caddy's
// own process can actually expand the {env.VAR} placeholders the daemon's
// emitted TLS config references. Values land only in this container's unit
// file (root-readable only, like any other systemd secret) — never in the
// daemon's own logs. envVars empty (no DNS-01 configured, or nothing
// resolved) renders byte-identical to the unit this function replaced, so
// existing non-DNS-01 hosts see no change.
func caddyServiceUnit(envVars map[string]string) string {
	names := make([]string, 0, len(envVars))
	for name := range envVars {
		names = append(names, name)
	}
	sort.Strings(names)
	var envLines strings.Builder
	for _, name := range names {
		fmt.Fprintf(&envLines, "Environment=%s=%s\n", name, envVars[name])
	}

	return fmt.Sprintf(`[Unit]
Description=Caddy
After=network.target network-online.target
Requires=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
%sExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force
TimeoutStopSec=5s
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`, envLines.String())
}

// ensureCaddyModules re-installs Caddy on an ALREADY-provisioned
// container when the currently-running binary is missing the module
// the daemon's own DNS-01 provider needs — caught in review of #1916:
// EnsureCaddy's two "container already exists" paths called
// reconcileCaddyDNSEnv (below) to keep credentials in sync, but never
// re-ran setupCaddy/verifyCaddyModules, so an operator switching DNS-01
// providers on a host whose Caddy binary came from the xcaddy
