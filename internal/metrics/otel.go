package metrics

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/footprintai/containarium/distros/go/containariumotel"
	"github.com/footprintai/containarium/internal/reqrate"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// CollectorConfig holds configuration for the OTel metrics collector
// PeerMetrics represents metrics from a peer backend container.
type PeerMetrics struct {
	ContainerName    string
	BackendID        string
	CPUUsageSeconds  int64
	MemoryUsageBytes int64
	DiskUsageBytes   int64
	NetworkRxBytes   int64
	NetworkTxBytes   int64
	ProcessCount     int64
}

// PeerSystemMetrics represents system-level metrics from a peer backend.
type PeerSystemMetrics struct {
	BackendID         string
	TotalCPUs         int64
	TotalMemoryBytes  int64
	UsedMemoryBytes   int64
	TotalDiskBytes    int64
	UsedDiskBytes     int64
	CPULoad1Min       float64
	CPULoad5Min       float64
	CPULoad15Min      float64
	ContainersRunning int64
	ContainersStopped int64
	// StoragePools is the peer's per-pool fill from its SystemInfo (#2823).
	StoragePools []incus.StoragePoolUsage
}

// PeerBackendHealth represents the health status of a backend instance.
type PeerBackendHealth struct {
	BackendID string
	Healthy   bool
	LastSeen  time.Time
}

// EgressFanoutStat is one container's egress fan-out for a collection tick —
// the crawler-detection signal. DistinctDestinations is the number of unique
// destination IPs the container connected out to; EgressConnections is the
// total egress connection count. ContainerID is the cloud_container_id tenant
// join key (empty on non-cloud boxes). Mirrors traffic.EgressStat at the
// metrics boundary so this package needn't import internal/traffic.
type EgressFanoutStat struct {
	ContainerName        string
	ContainerID          string
	DistinctDestinations int
	EgressConnections    int
}

// EgressFanoutFetcher reports per-container egress fan-out. Implemented by an
// adapter over the conntrack traffic collector; nil when conntrack monitoring
// is unavailable (e.g. macOS), in which case the plane stays dark.
type EgressFanoutFetcher interface {
	EgressFanout() []EgressFanoutStat
}

// PeerMetricsFetcher fetches container and system metrics from peer backends.
type PeerMetricsFetcher interface {
	// FetchPeerMetrics returns container metrics from all healthy peers.
	FetchPeerMetrics(authToken string) []PeerMetrics
	// FetchPeerSystemMetrics returns system metrics from all healthy peers.
	FetchPeerSystemMetrics(authToken string) []PeerSystemMetrics
	// FetchPeerHealth returns health status of all peer backends.
	FetchPeerHealth() []PeerBackendHealth
}

type CollectorConfig struct {
	// VictoriaMetricsURL is the base URL for Victoria Metrics (e.g., "http://10.100.0.x:8428")
	VictoriaMetricsURL string

	// CollectionInterval is how often to collect metrics (default 30s)
	CollectionInterval time.Duration

	// ServiceName is the OTel service name (default "containarium")
	ServiceName string

	// LocalBackendID is this daemon's backend ID (used as backend.id label on local metrics)
	LocalBackendID string
}

// DefaultCollectorConfig returns a default collector configuration
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		CollectionInterval: 30 * time.Second,
		ServiceName:        "containarium",
	}
}

// Collector collects system and container metrics via OpenTelemetry
type Collector struct {
	config      CollectorConfig
	incusClient *incus.Client
	provider    *sdkmetric.MeterProvider
	shutdownFn  containariumotel.ShutdownFunc

	// System metric instruments
	systemCPUCount   otelmetric.Int64Gauge
	systemMemTotal   otelmetric.Int64Gauge
	systemMemUsed    otelmetric.Int64Gauge
	systemDiskTotal  otelmetric.Int64Gauge
	systemDiskUsed   otelmetric.Int64Gauge
	systemCPULoad1m  otelmetric.Float64Gauge
	systemCPULoad5m  otelmetric.Float64Gauge
	systemCPULoad15m otelmetric.Float64Gauge

	// Container metric instruments
	containerCPUUsage     otelmetric.Int64Gauge
	containerMemUsage     otelmetric.Int64Gauge
	containerDiskUsage    otelmetric.Int64Gauge
	containerNetRx        otelmetric.Int64Gauge
	containerNetTx        otelmetric.Int64Gauge
	containerProcessCount otelmetric.Int64Gauge
	containerRequestRate  otelmetric.Float64Gauge

	// Egress fan-out instruments (crawler detection, #231 follow-on).
	containerEgressDistinctDest otelmetric.Int64Gauge
	containerEgressConnections  otelmetric.Int64Gauge

	// Aggregate instruments
	containersRunning otelmetric.Int64Gauge
	containersStopped otelmetric.Int64Gauge

	// Backend health instruments
	backendHealthy otelmetric.Int64Gauge

	// peerMTLS reports whether daemon-to-peer calls use peer mTLS (#3079).
	peerMTLS   otelmetric.Int64Gauge
	peerMTLSFn func() (enabled, relevant bool)

	ctx           context.Context
	cancel        context.CancelFunc
	peerFetcher   PeerMetricsFetcher
	egressFetcher EgressFanoutFetcher
	cpuHook       func(at time.Time, readings []ContainerCPUReading)
	peerPoolHook  func(backendID string, pools []incus.StoragePoolUsage)
}

// ContainerCPUReading is one local running container's cumulative CPU
// counter as read on a collection tick, plus what a consumer needs to join
// it to flows and to its CPU limit (#2635).
type ContainerCPUReading struct {
	Name            string
	IPAddress       string
	CPULimit        string // ContainerInfo.CPU: "4", "250m", "" when unlimited
	CPUUsageSeconds int64
}

// NewCollector creates a new OTel metrics collector
func NewCollector(config CollectorConfig, incusClient *incus.Client) (*Collector, error) {
	if config.VictoriaMetricsURL == "" {
		return nil, fmt.Errorf("VictoriaMetricsURL is required")
	}
	if config.CollectionInterval == 0 {
		config.CollectionInterval = 30 * time.Second
	}
	if config.ServiceName == "" {
		config.ServiceName = "containarium"
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Dogfood the Go distro for SDK setup (Phase 7 of the rollout
	// plan). The distro composes the resource per the standard
	// precedence + adds the defended containarium.distro stamp;
	// otlpmetrichttp.WithEndpointURL handles VictoriaMetrics' custom
	// ingest path under WithEndpoint.
	vmURL := stripProtocol(config.VictoriaMetricsURL)
	shutdownFn, err := containariumotel.Init(ctx,
		containariumotel.WithServiceName(config.ServiceName),
		containariumotel.WithEndpoint(fmt.Sprintf("http://%s/opentelemetry/api/v1/push", vmURL)),
		containariumotel.WithMetricInterval(config.CollectionInterval),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("init telemetry distro: %w", err)
	}

	// Pull the concrete *sdkmetric.MeterProvider back out of the
	// global so existing call sites (pentest.NewManager, Meter()
	// callers below) keep working without a wider refactor.
	provider, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider)
	if !ok {
		cancel()
		return nil, fmt.Errorf("global MeterProvider is not *sdkmetric.MeterProvider (got %T)", otel.GetMeterProvider())
	}

	c := &Collector{
		config:      config,
		incusClient: incusClient,
		provider:    provider,
		shutdownFn:  shutdownFn,
		ctx:         ctx,
		cancel:      cancel,
	}

	if err := c.initInstruments(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to init instruments: %w", err)
	}

	return c, nil
}

// initInstruments creates the OTel metric instruments
func (c *Collector) initInstruments() error {
	meter := c.provider.Meter("containarium")

	var err error

	// System metrics
	c.systemCPUCount, err = meter.Int64Gauge("system.cpu.count",
		otelmetric.WithDescription("Number of CPU cores"))
	if err != nil {
		return err
	}

	c.systemMemTotal, err = meter.Int64Gauge("system.memory.total_bytes",
		otelmetric.WithDescription("Total system memory in bytes"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.systemMemUsed, err = meter.Int64Gauge("system.memory.used_bytes",
		otelmetric.WithDescription("Used system memory in bytes"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.systemDiskTotal, err = meter.Int64Gauge("system.disk.total_bytes",
		otelmetric.WithDescription("Total disk space in bytes"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.systemDiskUsed, err = meter.Int64Gauge("system.disk.used_bytes",
		otelmetric.WithDescription("Used disk space in bytes"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.systemCPULoad1m, err = meter.Float64Gauge("system.cpu.load_1m",
		otelmetric.WithDescription("CPU load average (1 minute)"))
	if err != nil {
		return err
	}

	c.systemCPULoad5m, err = meter.Float64Gauge("system.cpu.load_5m",
		otelmetric.WithDescription("CPU load average (5 minutes)"))
	if err != nil {
		return err
	}

	c.systemCPULoad15m, err = meter.Float64Gauge("system.cpu.load_15m",
		otelmetric.WithDescription("CPU load average (15 minutes)"))
	if err != nil {
		return err
	}

	// Container metrics
	c.containerCPUUsage, err = meter.Int64Gauge("container.cpu.usage_seconds",
		otelmetric.WithDescription("Container CPU usage in seconds"),
		otelmetric.WithUnit("s"))
	if err != nil {
		return err
	}

	c.containerMemUsage, err = meter.Int64Gauge("container.memory.usage_bytes",
		otelmetric.WithDescription("Container memory usage in bytes"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.containerDiskUsage, err = meter.Int64Gauge("container.disk.usage_bytes",
		otelmetric.WithDescription("Container disk usage in bytes"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.containerNetRx, err = meter.Int64Gauge("container.network.rx_bytes",
		otelmetric.WithDescription("Container network bytes received"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.containerNetTx, err = meter.Int64Gauge("container.network.tx_bytes",
		otelmetric.WithDescription("Container network bytes transmitted"),
		otelmetric.WithUnit("By"))
	if err != nil {
		return err
	}

	c.containerProcessCount, err = meter.Int64Gauge("container.process.count",
		otelmetric.WithDescription("Number of running processes in container"))
	if err != nil {
		return err
	}

	// Request-rate plane (#231): edge-measured HTTP requests/sec per container.
	// → VM series container_request_rate{container_id=...}, the same join key
	// the bytes plane uses, consumed by the cloud's RequestRatePanel. Recorded
	// only when access-log aggregation is enabled (see RecordRequestRates and
	// docs/REQUEST-RATE-PLANE-DESIGN.md); the instrument is created
	// unconditionally so the wiring slice has nothing to bootstrap.
	c.containerRequestRate, err = meter.Float64Gauge("container.request_rate",
		otelmetric.WithDescription("Container HTTP requests per second (edge-measured)"),
		otelmetric.WithUnit("1/s"))
	if err != nil {
		return err
	}

	// Egress fan-out (#231 follow-on): the crawler-detection signal. A large,
	// churning count of distinct outbound destinations is a crawler's hallmark;
	// a normal app talks to a handful. Per-container aggregates only — the raw
