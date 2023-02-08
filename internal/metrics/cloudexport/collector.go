package cloudexport

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/footprintai/containarium/internal/metrics/platformstats"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// meterName is the instrumentation scope all exported host series are
// recorded under.
const meterName = "github.com/footprintai/containarium/internal/metrics/cloudexport"

// MinIntervalSeconds is the hard floor on the export cadence. Custom
// metrics are billed per ingested sample, so a misconfigured or hostile
// sub-minute interval must not be honored — the collector clamps up to
// this floor regardless of what config asks for.
const MinIntervalSeconds = 60

// Instrument names — the complete, allowlisted host-series set for
// #1070. Additions require touching this list (and its golden test),
// which is exactly the cost-surface review gate the design doc asks for.
const (
	MetricCPULoad1m      = "containarium.host.cpu.load_1m"
	MetricCPULoad5m      = "containarium.host.cpu.load_5m"
	MetricCPULoad15m     = "containarium.host.cpu.load_15m"
	MetricMemoryUsed     = "containarium.host.memory.used_bytes"
	MetricMemoryTotal    = "containarium.host.memory.total_bytes"
	MetricDiskUsed       = "containarium.host.disk.used_bytes"
	MetricDiskTotal      = "containarium.host.disk.total_bytes"
	MetricContainerCount = "containarium.host.container.count"

	// MetricContainerCPUUsageSeconds / MemoryUsageBytes / DiskUsageBytes /
	// NetworkRxBytes / NetworkTxBytes are the container group's
	// per-container series (#1071): the same GetAllMetrics data the
	// daemon's internal pipeline already collects, routed to the
	// cloud-provider sink so one noisy container is distinguishable from
	// host-level pressure. CPU/network are cumulative counters (matching
	// incus's cumulative CPU-seconds and byte counters); memory/disk are
	// point-in-time gauges. Collection re-enumerates live containers every
	// tick, so a deleted container simply stops appearing — no separate
	// deletion bookkeeping.
	MetricContainerCPUUsageSeconds  = "containarium.container.cpu.usage_seconds"
	MetricContainerMemoryUsageBytes = "containarium.container.memory.usage_bytes"
	MetricContainerDiskUsageBytes   = "containarium.container.disk.usage_bytes"
	MetricContainerNetworkRxBytes   = "containarium.container.network.rx_bytes"
	MetricContainerNetworkTxBytes   = "containarium.container.network.tx_bytes"

	// MetricContainerCPUCFSPeriods / ThrottledPeriods / ThrottledUsec are
	// the container group's CFS-throttling series (#1575), the alertable
	// half of #1573. cpu.usage_seconds above cannot distinguish a box
	// pinned at its CFS quota from an idle one — both read as a low
	// number — so it is the one CPU series exported and it has exactly
	// the blind spot the counters exist to close.
	//
	// Both the numerator and the denominator are exported because the
	// alert worth writing is a *ratio* ("throttled >30% of its periods
	// for 15 minutes"), which cannot be reconstructed from the throttled
	// count alone. All three are cumulative counters, matching the
	// container's own cgroup accounting and the usage/network series
	// beside them.
	//
	// A box whose runtime reports no signal (K8s boxes, a cgroup with no
	// bandwidth limit, a stopped container) has NO point observed rather
	// than a zero — see registerContainerInstruments.
	MetricContainerCPUCFSPeriods       = "containarium.container.cpu.cfs_periods"
	MetricContainerCPUThrottledPeriods = "containarium.container.cpu.throttled_periods"
	MetricContainerCPUThrottledUsec    = "containarium.container.cpu.throttled_usec"

	// MetricHeartbeat is the liveness/up series (#1072): a constant 1
	// emitted every export interval while the daemon runs. It is the
	// out-of-band dead-man signal — a metric-absence alert policy in the
	// cloud provider's monitoring fires when this series stops arriving,
	// catching the failure class (host or daemon dead / network-
	// partitioned) that host-local alerting cannot report on because it
	// fate-shares with the host.
	MetricHeartbeat = "containarium.export.heartbeat"

	// MetricPlatformAPIRequests / MetricPlatformAPIErrors are the
	// platform group's API-health series (#1082): cumulative counts of
	// completed API calls (native gRPC + REST-via-grpc-gateway, both
	// converge on the same interceptor), by coarse code_class. Requests
	// counts every completed call; errors counts only the client_error
	// and server_error classes.
	MetricPlatformAPIRequests = "containarium.platform.api.requests"
	MetricPlatformAPIErrors   = "containarium.platform.api.errors"

	// MetricPlatformProvisionAttempts / Failures / DurationSecondsSum are
	// the platform group's provisioning-outcome series (#1083): a
	// container create or delete, by operation. Attempts counts every
	// attempt (success or not); Failures counts the subset that failed;
	// DurationSecondsSum is the cumulative wall-clock time spent across
	// all attempts for that operation, regardless of outcome — dividing
	// it by Attempts in MQL/PromQL gives mean latency without a
	// per-bucket-billed histogram.
	MetricPlatformProvisionAttempts           = "containarium.platform.provision.attempts"
	MetricPlatformProvisionFailures           = "containarium.platform.provision.failures"
	MetricPlatformProvisionDurationSecondsSum = "containarium.platform.provision.duration_seconds_sum"

	// MetricPlatformPeersConnected / MetricPlatformTunnelState are the
	// platform group's connectivity series (#1084): how many registered
	// BYOC peers this backend currently sees as healthy, and each one's
	// individual tunnel up/down state. Complements #1078 (BYOC hosts
	// can't export themselves) — the GCP primary exports connectivity on
	// their behalf.
	MetricPlatformPeersConnected = "containarium.platform.peers.connected"
	MetricPlatformTunnelState    = "containarium.platform.tunnel.state"

	// MetricBackupLastSuccessAgeSeconds is the backup-health series
	// (#2294): seconds elapsed since each tenant's most recent
	// successful backup, recomputed fresh at every export tick from the
	// daemon's own durable backup index — not cached in-memory state, so
	// it survives a daemon restart unlike a naive "record at success
	// time" counter would. A straightforward conditionThreshold alert
	// ("age > expected interval + slack") catches a tenant's scheduled
	// backup silently breaking (disabled timer, wrong hook path,
	// misconfigured conf line) even while the daemon itself — and its
	// own containarium.export.heartbeat — stays perfectly healthy; that
	// is the gap this series closes, distinct from (and a supplement to)
	// the heartbeat's whole-host dead-man coverage. See
	// docs/METRICS-EXPORT-DEADMAN-ALERT-RUNBOOK.md's backup-alert
	// section for the policy JSON.
	MetricBackupLastSuccessAgeSeconds = "containarium.backup.last_success_age_seconds"

	// MetricBackupHealthReadFailures is the self-health companion to the
	// series above (#2305): a cumulative count of export ticks on which
	// the daemon could not read its backup index. When that read fails,
	// last_success_age_seconds goes empty for every tenant at once — the
	// same shape as "nothing has ever been backed up" — so an alert on
	// the age series alone cannot see the collector break. Observed on
	// every tick including at zero, so a present, flat series means
	// "reading fine" and an increasing one means "read is failing"; alert
	// on increase > 0.
	MetricBackupHealthReadFailures = "containarium.backup.health_read_failures"
)

// Label keys — the complete allowlist. No org/tenant identifier ever
// appears here; the labels are fixed daemon identity set once at
// construction, so a Sources implementation has no channel to inject a
// tenant label through the numeric snapshot it returns.
const (
	LabelBackendID = "backend_id"
	LabelHostname  = "hostname"
	LabelRegion    = "region"
	// LabelDaemonVersion tags the heartbeat series only (not the host
	// series) with the daemon build, so a dead-man alert makes clear which
	// version stopped reporting.
	LabelDaemonVersion = "daemon_version"
	// LabelCodeClass tags the platform API series with their coarse
	// outcome bucket (platformstats.CodeClass) — the deliberately small,
	// fixed-cardinality dimension the design uses instead of a raw route
	// or gRPC code, which would blow up the billed cost surface.
	LabelCodeClass = "code_class"
	// LabelOperation tags the platform provisioning series with which
	// kind of provisioning call this was (platformstats.Operation) —
	// create or delete, never a per-request identifier.
	LabelOperation = "operation"
	// LabelPeerID tags the tunnel.state series with which registered
	// peer a point is for (#1084). Always the enrolled host name — the
	// enrollment path forbids org/tenant identifiers in it — never an
	// org/tenant identifier itself.
	LabelPeerID = "peer_id"
	// LabelContainerName tags the container-group series with which
	// container a point is for (#1071). Deliberately the ONLY per-point
	// label alongside backend_id — the container group's label set is
	// narrower than host/platform (no hostname/region), per the design
	// doc's allowlist.
	LabelContainerName = "container_name"
	// LabelUsername tags the backup-health series with which tenant a
	// point is for (#2294) — see BackupHealthState for why this is a
	// deliberate, reviewed exception to every other label in this file
	// being tenant-identifier-free. No other series may use this label;
	// TestNoTenantLabels_* for every other series still asserts its
	// absence there.
	LabelUsername = "username"
)

// Labels is the fixed identity stamped on every exported series. These
// are host-level facts (which backend, which machine, which region, which
// daemon build), not per-tick data — so they live on the collector, not
// in Sources. Not every series carries every field: the host series use
// backend_id/hostname/region; the heartbeat uses backend_id/hostname/
// daemon_version.
type Labels struct {
	BackendID     string
	Hostname      string
	Region        string
	DaemonVersion string
}

// attributeSet is the host-series label set (backend_id, hostname,
// region).
func (l Labels) attributeSet() attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelHostname, l.Hostname),
		attribute.String(LabelRegion, l.Region),
	)
}

// heartbeatAttributeSet is the heartbeat label set (backend_id, hostname,
// daemon_version) — deliberately distinct from the host-series set: the
// dead-man alert cares which daemon build went silent, not which region.
func (l Labels) heartbeatAttributeSet() attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelHostname, l.Hostname),
		attribute.String(LabelDaemonVersion, l.DaemonVersion),
	)
}

// platformAttributeSet is the platform API-health label set (backend_id,
// hostname, region, code_class) — the host-series identity plus the
// per-point outcome class.
func (l Labels) platformAttributeSet(class platformstats.CodeClass) attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelHostname, l.Hostname),
		attribute.String(LabelRegion, l.Region),
		attribute.String(LabelCodeClass, string(class)),
	)
}

// provisionAttributeSet is the platform provisioning-outcome label set
// (backend_id, hostname, region, operation) — the host-series identity
// plus which operation this point is for.
func (l Labels) provisionAttributeSet(op platformstats.Operation) attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelHostname, l.Hostname),
		attribute.String(LabelRegion, l.Region),
		attribute.String(LabelOperation, string(op)),
	)
}

// tunnelAttributeSet is the platform tunnel-state label set (backend_id,
// hostname, region, peer_id) — the host-series identity plus which peer
// this point is for (#1084).
func (l Labels) tunnelAttributeSet(peerID string) attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelHostname, l.Hostname),
		attribute.String(LabelRegion, l.Region),
		attribute.String(LabelPeerID, peerID),
	)
}

// containerAttributeSet is the container-series label set (backend_id,
// container_name) (#1071) — deliberately narrower than every other
// group's label set: no hostname/region, per the design doc's allowlist.
func (l Labels) containerAttributeSet(containerName string) attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelContainerName, containerName),
	)
}

// backupAttributeSet is the backup-health label set (backend_id,
// hostname, region, username) (#2294) — the host-series identity plus
// which tenant this point is for. See LabelUsername for why this one
// series carries a tenant identifier when nothing else here does.
func (l Labels) backupAttributeSet(username string) attribute.Set {
	return attribute.NewSet(
		attribute.String(LabelBackendID, l.BackendID),
		attribute.String(LabelHostname, l.Hostname),
		attribute.String(LabelRegion, l.Region),
		attribute.String(LabelUsername, username),
	)
}

// CollectorOptions are the construction inputs for a CloudExportCollector.
type CollectorOptions struct {
	// Sources is the seam over the daemon's metric collection. Required.
	Sources Sources
	// PlatformSources is the read-side seam over platform-domain facts
	// (#1082/#1083/#1084). Optional — nil means the platform group, if
	// enabled, registers no instruments (the same "reserved" behavior
	// container/platform had before any of those issues landed), rather
	// than erroring.
	PlatformSources PlatformSources
	// Exporter is the OTel SDK metric exporter to push batches through
	// (a provider Sink's NewExporter result in production, a fake in
	// tests). Required.
	Exporter sdkmetric.Exporter
	// Resource is the OTel resource the series are exported under: the
	// provider's identity attributes (e.g. the instance id and zone from the
	// GCP detector). Which monitored resource Cloud Monitoring records for
	// them is the provider's own mapping, not this field's (for GCP OTLP
	// since v0.100.0 it is prometheus_target, not gce_instance). Optional;
	// nil falls back to the SDK default resource.
	Resource *resource.Resource
	// Labels is the fixed backend_id/hostname/region identity.
	Labels Labels
	// IntervalSeconds is the requested export cadence. Clamped up to
	// MinIntervalSeconds; <= 0 uses MinIntervalSeconds.
	IntervalSeconds int32
	// Groups selects which independently-enableable series groups this
	// collector registers (#1081). Normalized at construction: an empty
	// selection resolves to [HOST], so a collector built from a v0.60.0
	// config exports exactly the #1070 host series.
	Groups []pb.CloudMetricsGroup
	// Now is the clock the backup-health series (#2294) reads to turn a
	// tenant's last-success timestamp into an age. Test seam; nil
	// defaults to time.Now in production.
	Now func() time.Time
}

// healthState is the collector's live export health, surfaced through
// GetMetricsExport. Guarded by its own mutex because it is written from
// the PeriodicReader's background goroutine and read from RPC handlers.
type healthState struct {
	mu             sync.Mutex
	lastSuccessAt  time.Time
	lastError      string
	exportFailures int64
}

func (h *healthState) recordSuccess(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastSuccessAt = now
	h.lastError = ""
}

func (h *healthState) recordFailure(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.exportFailures++
	h.lastError = err.Error()
}

func (h *healthState) snapshot() (lastSuccessAt time.Time, lastError string, failures int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSuccessAt, h.lastError, h.exportFailures
}

// healthExporter decorates the real exporter to record per-batch
// success/failure into the shared healthState. It never changes the
// exporter's own error behavior — the OTel SDK still logs and drops on
// its own terms; this only observes the outcome so GetMetricsExport can
// report real numbers.
type healthExporter struct {
	sdkmetric.Exporter
	health *healthState
	now    func() time.Time
}

func (h *healthExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	err := h.Exporter.Export(ctx, rm)
	if err != nil {
		h.health.recordFailure(err)
	} else {
		h.health.recordSuccess(h.now())
	}
	return err
}

// CloudExportCollector owns a dedicated OTel MeterProvider + a
// PeriodicReader wired to one provider exporter, and registers exactly
// the allowlisted host-series instruments as async gauges pulling from
// Sources. It is deliberately a second, separate pipeline from the
// daemon's internal Collector: the exported instrument set is the billed
// cost surface, and keeping it isolated makes that surface explicit and
// reviewable in one file.
type CloudExportCollector struct {
	sources         Sources
	platformSources PlatformSources
	exporter        sdkmetric.Exporter
	resource        *resource.Resource
	labels          Labels
	interval        time.Duration
	groups          []pb.CloudMetricsGroup
	health          *healthState
	now             func() time.Time

	mu      sync.Mutex
	mp      *sdkmetric.MeterProvider
	started bool
}

// NewCollector builds an unstarted CloudExportCollector. Call Start to
// begin periodic export and Stop to tear it down; both are idempotent.
func NewCollector(opts CollectorOptions) *CloudExportCollector {
	interval := time.Duration(opts.IntervalSeconds) * time.Second
	if floor := MinIntervalSeconds * time.Second; interval < floor {
		interval = floor
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &CloudExportCollector{
		sources:         opts.Sources,
		platformSources: opts.PlatformSources,
		exporter:        opts.Exporter,
		resource:        opts.Resource,
		labels:          opts.Labels,
		interval:        interval,
		groups:          NormalizeGroups(opts.Groups),
		health:          &healthState{},
		now:             now,
	}
}

// Start begins periodic export. Idempotent: a second call while running
// is a no-op. The MeterProvider it stands up is entirely separate from
// the daemon's internal metrics pipeline.
func (c *CloudExportCollector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return nil
	}

	reader := sdkmetric.NewPeriodicReader(
		&healthExporter{Exporter: c.exporter, health: c.health, now: time.Now},
		sdkmetric.WithInterval(c.interval),
	)
	mp, err := c.buildMeterProvider(reader)
	if err != nil {
		_ = reader.Shutdown(ctx)
		return err
	}
	c.mp = mp
	c.started = true
	return nil
}

// Stop tears down the MeterProvider (final flush + reader shutdown) and
// halts emission. Idempotent: a call while already stopped is a no-op.
func (c *CloudExportCollector) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return nil
	}
	mp := c.mp
	c.mp = nil
	c.started = false
	if mp == nil {
		return nil
	}
	return mp.Shutdown(ctx)
}

// ForceFlush triggers an immediate collection + export outside the
// periodic cadence. Used by tests to observe a tick without waiting a
// full interval; a no-op when the collector is stopped.
func (c *CloudExportCollector) ForceFlush(ctx context.Context) error {
	c.mu.Lock()
	mp := c.mp
	c.mu.Unlock()
	if mp == nil {
		return nil
	}
	return mp.ForceFlush(ctx)
}

// Health reports the live export health for GetMetricsExport.
func (c *CloudExportCollector) Health() (lastSuccessAt time.Time, lastError string, exportFailures int64) {
	return c.health.snapshot()
}

// buildMeterProvider stands up the MeterProvider on the given reader and
// registers the allowlisted instruments with a single multi-instrument
// callback. Factored out (unexported) so unit tests can drive it with a
// ManualReader and assert the exact series/label set the callback emits.
func (c *CloudExportCollector) buildMeterProvider(reader sdkmetric.Reader) (*sdkmetric.MeterProvider, error) {
	mpOpts := []sdkmetric.Option{sdkmetric.WithReader(reader)}
	if c.resource != nil {
		mpOpts = append(mpOpts, sdkmetric.WithResource(c.resource))
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)
	for _, g := range c.groups {
		if err := registerGroupInstruments(mp, g, c.sources, c.platformSources, c.labels, c.now); err != nil {
			return nil, err
		}
	}
	if err := registerHeartbeatInstrument(mp, c.labels); err != nil {
		return nil, err
	}
	return mp, nil
}

// registerGroupInstruments registers exactly the instruments belonging
// to one metric group (#1081). This is the single dispatch point new
// groups plug into: host registers the #1070 allowlist; container
// registers the #1071 per-container series; platform registers the
// #1082/#1083/#1084 API-health, provisioning-outcome, and connectivity
// series when a PlatformSources is wired, and nothing otherwise. Groups
// are normalized before they reach here, so UNSPECIFIED never arrives;
// an unknown value is a programming error.
//
// Note the heartbeat is deliberately NOT a group: it is registered
// unconditionally by buildMeterProvider alongside (and independent of)
// whatever groups are enabled, because the dead-man signal must never be
// gated by group selection or a Sources error (see
// registerHeartbeatInstrument).
func registerGroupInstruments(mp *sdkmetric.MeterProvider, group pb.CloudMetricsGroup, sources Sources, platformSources PlatformSources, labels Labels, now func() time.Time) error {
	switch group {
	case pb.CloudMetricsGroup_CLOUD_METRICS_GROUP_HOST:
		return registerHostInstruments(mp, sources, labels)
	case pb.CloudMetricsGroup_CLOUD_METRICS_GROUP_CONTAINER:
		return registerContainerInstruments(mp, sources, labels)
	case pb.CloudMetricsGroup_CLOUD_METRICS_GROUP_PLATFORM:
		if platformSources == nil {
			// Not wired (e.g. an older daemon build, or a test that
			// doesn't need it) — deliberate zero-series opt-in, same as
			// the pre-#1082 "reserved" behavior, never an error.
			return nil
		}
		return registerPlatformInstruments(mp, platformSources, labels, now)
	default:
		return fmt.Errorf("cloudexport: unregisterable metric group %v", group)
	}
}

// registerPlatformInstruments creates the platform group's API-health
// (#1082), provisioning-outcome (#1083), connectivity (#1084), and
// backup-health (#2294) instruments and wires one callback per concern,
// each pulling a single snapshot per tick. API-health and provisioning
// are counters: OTel async-counter semantics expect the current
// cumulative total on every observation (platformstats.Stats already
// accumulates for the daemon's lifetime), which is exactly what those
// snapshots report. Connectivity and backup-health are gauges: both are
// current state (peer health; seconds since last success), not an
// accumulating count.
func registerPlatformInstruments(mp *sdkmetric.MeterProvider, sources PlatformSources, labels Labels, now func() time.Time) error {
	meter := mp.Meter(meterName)

	requests, err := meter.Int64ObservableCounter(MetricPlatformAPIRequests,
		metric.WithDescription("Cumulative count of completed API requests (gRPC + REST-via-gateway), by coarse outcome class."))
	if err != nil {
		return err
	}
	apiErrors, err := meter.Int64ObservableCounter(MetricPlatformAPIErrors,
		metric.WithDescription("Cumulative count of completed API requests that resulted in a client or server error, by coarse outcome class."))
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			snap := sources.APIStats()
			for class, n := range snap.RequestsByClass {
				o.ObserveInt64(requests, n, metric.WithAttributeSet(labels.platformAttributeSet(class)))
			}
			for class, n := range snap.ErrorsByClass {
				o.ObserveInt64(apiErrors, n, metric.WithAttributeSet(labels.platformAttributeSet(class)))
			}
			return nil
		},
		requests, apiErrors,
	)
	if err != nil {
		return err
	}

	attempts, err := meter.Int64ObservableCounter(MetricPlatformProvisionAttempts,
		metric.WithDescription("Cumulative count of container provisioning attempts (create/delete), by operation."))
	if err != nil {
		return err
	}
	failures, err := meter.Int64ObservableCounter(MetricPlatformProvisionFailures,
		metric.WithDescription("Cumulative count of container provisioning attempts that failed, by operation."))
	if err != nil {
		return err
	}
	durationSum, err := meter.Float64ObservableCounter(MetricPlatformProvisionDurationSecondsSum,
		metric.WithUnit("s"), metric.WithDescription("Cumulative wall-clock time spent on provisioning attempts, by operation — divide by attempts for mean latency."))
	if err != nil {
		return err
	}

	_, err = meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			snap := sources.ProvisionStats()
			for op, n := range snap.Attempts {
				o.ObserveInt64(attempts, n, metric.WithAttributeSet(labels.provisionAttributeSet(op)))
