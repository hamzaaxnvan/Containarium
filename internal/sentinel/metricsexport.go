package sentinel

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/footprintai/containarium/internal/metrics/cloudexport"
)

// Sentinel metrics export (#1358).
//
// #1351 put process-health series on /metrics and #1358's first half added the
// failover series, but nothing scrapes that endpoint — and nothing on the spot
// VM can. The bundled monitoring stack (VictoriaMetrics + vmalert +
// Alertmanager) runs in a container ON the backend, so it dies with the very
// outage worth observing. That is the same argument observability.go already
// makes for why the sentinel owns the preemption signal; it applies just as
// well to the sentinel's own health.
//
// So the always-on sentinel pushes to Cloud Monitoring itself. Push, not
// scrape: it needs no inbound firewall rule for the internal-only binary-server
// port, and it keeps reporting while the backend is gone.
//
// Only the Sink is reused from the daemon's export path. The
// CloudExportCollector itself is daemon-shaped — its Sources interface wants
// per-container metrics and container counts, it is constructed from
// ContainerServer config the sentinel has no equivalent of, and its instrument
// names are a deliberately allowlisted set gated by a golden test. Threading
// the sentinel through all of that would mean a proto enum change and a much
// larger blast radius than a self-contained meter here.
//
// COST SURFACE. Custom metrics are billed per ingested sample. At the 60s
// floor this is 8 fixed series per sentinel plus one per backend
// (backend_healthy) — roughly 12 series/minute on a sentinel fronting four
// backends. Additions belong in this list, deliberately, for the same
// review-gate reason the daemon's allowlist exists.

const (
	metricSentinelState          = "containarium.sentinel.state"
	metricSentinelPreempted      = "containarium.sentinel.preempted_total"
	metricSentinelRecovered      = "containarium.sentinel.recovered_total"
	metricSentinelFailover       = "containarium.sentinel.failover_total"
	metricSentinelBackendHealthy = "containarium.sentinel.backend_healthy"
	metricSentinelPrimaryPref    = "containarium.sentinel.primary_preferred"
	metricSentinelRSS            = "containarium.sentinel.process.resident_memory_bytes"
	metricSentinelGoroutines     = "containarium.sentinel.process.goroutines"
	metricSentinelOpenFDs        = "containarium.sentinel.process.open_fds"

	// sentinelMeterName is the instrumentation scope for these series.
	sentinelMeterName = "github.com/footprintai/containarium/internal/sentinel"
)

// MetricsExportOptions configures the sentinel's push exporter.
type MetricsExportOptions struct {
	// Exporter is the OTel SDK exporter to push through — in production a
	// cloudexport Sink's NewExporter result. Required.
	Exporter sdkmetric.Exporter

	// Resource tags every series with the provider's monitored-resource
	// identity (gce_instance on GCP). Optional; nil falls back to the SDK
	// default, which still exports but loses the per-instance association.
	Resource *resource.Resource

	// SentinelID distinguishes one sentinel's series from another's. Without
	// it two sentinels' timeseries collide into one meaningless line.
	SentinelID string

	// IntervalSeconds is the requested cadence, clamped up to the billing
	// floor.
	IntervalSeconds int32
}

