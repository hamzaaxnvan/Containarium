package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	appconfig "github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/pkg/core/incus"
)

const (
	// CoreOTelCollectorContainer is the name of the core OTel collector
	// LXC. It receives app-emitted OTLP from monitoring=true containers
	// and forwards to the local VictoriaMetrics via OTLP/HTTP.
	CoreOTelCollectorContainer = "containarium-core-otelcollector"

	// DefaultOTelOTLPHTTPPort is the OTLP/HTTP receiver port.
	DefaultOTelOTLPHTTPPort = 4318

	// DefaultOTelOTLPGRPCPort is the OTLP/gRPC receiver port.
	DefaultOTelOTLPGRPCPort = 4317

	// otelCollectorVersion pins the upstream contrib release. Keep in
	// sync with sidecars/otel-sidecar/Dockerfile's OTELCOL_VERSION
	// (decision O1) — see OSS #2164 for why this matters: an old pin
	// silently accumulates CVEs in the embedded Go toolchain/deps even
	// though nothing in this repo changed.
	otelCollectorVersion = "0.162.0"

	// containerIPsFile is the path inside the collector LXC where
	// the daemon pushes the source-IP → container-name map. The
	// collector's filewatcher (v2) will hot-reload from it; in v1
	// the daemon regenerates the transform processor's OTTL
	// statements directly in config.yaml.
	containerIPsFile = "/var/lib/containarium/container_ips.json"
)

// DefaultOTelDropLabels is the cardinality guard's default drop-list.
// These attributes most often blow up TSDBs in practice (per-request,
// per-user, per-trace IDs). Operators can extend the list with
// --otel-drop-labels=a,b,c at daemon startup; this default is always
// applied. Per docs/OTEL-COLLECTOR-DESIGN.md §5.
var DefaultOTelDropLabels = []string{
	"request_id",
	"trace_id",
	"user_email",
	"session_id",
	"correlation_id",
}

// EnsureOTelCollector ensures the OTel collector container exists, is
// running, and has its config pointed at the provided VictoriaMetrics
// IP. Idempotent — safe to call on every daemon startup.
//
// dropLabels is the union of DefaultOTelDropLabels and any
// operator-provided extras (via --otel-drop-labels). The merged list
// is fed to the transform processor's OTTL delete_matching_keys
// statement.
func (cs *CoreServices) EnsureOTelCollector(ctx context.Context, victoriaMetricsIP string, dropLabels []string) (string, error) {
	if victoriaMetricsIP == "" {
		return "", fmt.Errorf("VictoriaMetrics IP required (cannot configure collector exporter without it)")
	}

	// Reserve 1GB so the collector keeps draining metrics even when
	// user containers fill the pool.
	cs.ensureCoreReservation(CoreOTelCollectorContainer, "1G")

	merged := mergeOTelDropLabels(DefaultOTelDropLabels, dropLabels)

	info, err := cs.incusClient.GetContainer(CoreOTelCollectorContainer)
	if err == nil {
		cs.backfillConfig(CoreOTelCollectorContainer, incus.RoleOTelCollector, "75")

		if info.State == "Running" {
			cs.otelCollectorIP = info.IPAddress
			log.Printf("OTel collector container already running at %s", cs.otelCollectorIP)
			// Always refresh config on re-run so VM IP or operator
			// drop-label changes propagate without a recreate.
			if err := cs.applyOTelCollectorConfig(victoriaMetricsIP, merged); err != nil {
				log.Printf("Warning: failed to refresh OTel collector config: %v", err)
			}
			return cs.otelCollectorIP, nil
		}

		log.Printf("Starting existing OTel collector container...")
		if err := cs.incusClient.StartContainer(CoreOTelCollectorContainer); err != nil {
			return "", fmt.Errorf("failed to start otel collector: %w", err)
		}
		ip, err := cs.incusClient.WaitForNetwork(CoreOTelCollectorContainer, 60*time.Second)
		if err != nil {
			return "", fmt.Errorf("failed to get otel collector IP: %w", err)
		}
		cs.otelCollectorIP = ip
		if err := cs.applyOTelCollectorConfig(victoriaMetricsIP, merged); err != nil {
			log.Printf("Warning: failed to refresh OTel collector config: %v", err)
		}
		return cs.otelCollectorIP, nil
	}

	log.Printf("Creating OTel collector container...")

	config := incus.ContainerConfig{
		Name:      CoreOTelCollectorContainer,
		Image:     "images:ubuntu/24.04",
		CPU:       "1",
		Memory:    "512MB",
		AutoStart: true,
		Disk: &incus.DiskDevice{
			Path: "/",
			Pool: cs.incusClient.StoragePool(),
			Size: "3GB",
		},
	}
	cs.pinCoreIP(&config, CoreOTelCollectorContainer)

	if err := cs.incusClient.CreateContainer(config); err != nil {
		return "", fmt.Errorf("failed to create otel collector container: %w", err)
	}

	cs.setContainerConfigBestEffort(CoreOTelCollectorContainer, incus.RoleKey, string(incus.RoleOTelCollector))
	cs.setContainerConfigBestEffort(CoreOTelCollectorContainer, "boot.autostart", "true")
	cs.setContainerConfigBestEffort(CoreOTelCollectorContainer, "boot.autostart.priority", "75")

	if err := cs.incusClient.StartContainer(CoreOTelCollectorContainer); err != nil {
		return "", fmt.Errorf("failed to start otel collector container: %w", err)
	}
	ip, err := cs.incusClient.WaitForNetwork(CoreOTelCollectorContainer, 60*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to get otel collector IP: %w", err)
	}
	cs.otelCollectorIP = ip
	log.Printf("OTel collector container IP: %s", cs.otelCollectorIP)

	if err := cs.installOTelCollector(ctx, victoriaMetricsIP, merged); err != nil {
		return "", fmt.Errorf("failed to setup otel collector: %w", err)
	}

	cs.applyBaseScripts(CoreOTelCollectorContainer, "ubuntu")

	return cs.otelCollectorIP, nil
}

// installOTelCollector installs otelcol-contrib and writes the initial
// config. Runs only on first-time create; later restarts go through
// applyOTelCollectorConfig.
func (cs *CoreServices) installOTelCollector(ctx context.Context, vmIP string, dropLabels []string) error {
	log.Printf("Installing otelcol-contrib %s...", otelCollectorVersion)

	time.Sleep(5 * time.Second)

	prep := [][]string{
		{"apt-get", "update"},
		{"apt-get", "install", "-y", "curl", "wget", "ca-certificates"},
		{"mkdir", "-p", "/etc/otelcol-contrib", "/var/lib/containarium"},
	}
	for _, c := range prep {
		if err := cs.incusClient.Exec(CoreOTelCollectorContainer, c); err != nil {
			return fmt.Errorf("failed to run %v: %w", c, err)
		}
	}

	// Download the contrib release tarball and extract the binary
	// only — full release tarball is ~80MB with sample configs we
	// don't need.
	tgzURL := fmt.Sprintf(
		"https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v%s/otelcol-contrib_%s_linux_amd64.tar.gz",
		otelCollectorVersion, otelCollectorVersion,
	)
	dl := [][]string{
		{"bash", "-c", fmt.Sprintf("wget -qO /tmp/otelcol-contrib.tar.gz %s", tgzURL)},
		{"bash", "-c", "tar -xzf /tmp/otelcol-contrib.tar.gz -C /usr/local/bin/ otelcol-contrib"},
		{"chmod", "+x", "/usr/local/bin/otelcol-contrib"},
		{"rm", "-f", "/tmp/otelcol-contrib.tar.gz"},
	}
	for _, c := range dl {
		if err := cs.incusClient.Exec(CoreOTelCollectorContainer, c); err != nil {
			return fmt.Errorf("failed to install otelcol-contrib: %w", err)
		}
	}

	// Empty initial IP map — daemon pushes the real one once
	// container lifecycle events start firing.
	if err := cs.incusClient.WriteFile(CoreOTelCollectorContainer, containerIPsFile, []byte("{}\n"), "0644"); err != nil {
		return fmt.Errorf("failed to seed container_ips.json: %w", err)
	}

	if err := cs.applyOTelCollectorConfig(vmIP, dropLabels); err != nil {
		return fmt.Errorf("failed to write initial otel config: %w", err)
	}

	unit := `[Unit]
Description=OpenTelemetry Collector Contrib
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/otelcol-contrib --config=/etc/otelcol-contrib/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`
	if err := cs.incusClient.WriteFile(CoreOTelCollectorContainer, "/etc/systemd/system/otelcol-contrib.service", []byte(unit), "0644"); err != nil {
		return fmt.Errorf("failed to write otelcol-contrib systemd unit: %w", err)
	}

	start := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "otelcol-contrib"},
		{"systemctl", "restart", "otelcol-contrib"},
	}
	for _, c := range start {
		if err := cs.incusClient.Exec(CoreOTelCollectorContainer, c); err != nil {
			return fmt.Errorf("failed to start otelcol-contrib: %w", err)
		}
	}

	if err := cs.waitForOTelCollector(ctx); err != nil {
		return fmt.Errorf("otelcol-contrib not ready: %w", err)
	}

	log.Printf("OTel collector setup complete")
	return nil
}

// applyOTelCollectorConfig (re-)writes config.yaml with the supplied
// VM IP and drop-label set, then reloads the running collector via
// SIGHUP. Safe to call repeatedly; the collector picks up the new
// config without dropping in-flight batches.
//
// Phase 2.5 follow-up — when CONTAINARIUM_OTEL_REQUIRE_AUTH=true
// the config wires the bearertokenauth extension onto the OTLP
// receivers; every push must carry `Authorization: Bearer <secret>`.
// Default off so operators control the cutover: existing monitoring
// containers need to re-stamp the header (toggle off+on, or restart)
// before flipping enforcement on.
func (cs *CoreServices) applyOTelCollectorConfig(vmIP string, dropLabels []string) error {
	cfg := buildOTelCollectorConfig(vmIP, dropLabels, collectorBearerForConfig())
	if err := cs.incusClient.WriteFile(CoreOTelCollectorContainer, "/etc/otelcol-contrib/config.yaml", []byte(cfg), "0644"); err != nil {
		return fmt.Errorf("failed to write otelcol config: %w", err)
	}
	// best-effort reload — fine if the unit isn't up yet (first install).
	_ = cs.incusClient.Exec(CoreOTelCollectorContainer, []string{"systemctl", "reload-or-restart", "otelcol-contrib"})
	return nil
}

// collectorBearerForConfig returns the bearer to bake into
// the collector's config.yaml, or "" to skip enforcement.
// Empty when CONTAINARIUM_OTEL_REQUIRE_AUTH is unset / not
// recognized, or when the bearer load itself fails.
//
// Cutover sequence for operators:
//  1. Run the new daemon (bearer auto-created; header
//     stamped on new monitoring containers).
//  2. Re-toggle monitoring on existing containers so they
//     pick up the header (or restart, which re-stamps).
//  3. Set CONTAINARIUM_OTEL_REQUIRE_AUTH=true and restart
//     the daemon. Now the collector enforces; any
//     container without the header drops silently (and
//     that's the signal to find the laggard).
func collectorBearerForConfig() string {
	raw := strings.TrimSpace(os.Getenv(appconfig.EnvOTELRequireAuth))
	on := false
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		on = true
	}
	if !on {
		return ""
