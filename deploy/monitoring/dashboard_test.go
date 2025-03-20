// Package monitoring holds committed GCM configuration data (dashboard
// JSON, reproducible via `gcloud monitoring dashboards create`) rather
// than deployable Go code. This test file exists solely to pin the
// dashboard JSON against metric-name drift: every widget must reference
// one of cloudexport's actual exported instrument names, so a renamed
// or removed series is caught here instead of silently going stale in a
// committed artifact nobody re-generates.
package monitoring

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/metrics/cloudexport"
)

// counterInstruments are the exported instruments that are monotonic
// counters (collector.go registers them with Int64/Float64ObservableCounter).
// Since the OTLP exporter (#2008, v0.100.0) Cloud Monitoring stores a counter
// as prometheus.googleapis.com/<name>/counter (CUMULATIVE) and every other
// instrument as .../gauge, on a prometheus_target resource (#2608). A new
// counter added to the dashboard must be listed here.
var counterInstruments = map[string]bool{
	cloudexport.MetricPlatformAPIRequests:                 true,
	cloudexport.MetricPlatformAPIErrors:                   true,
	cloudexport.MetricPlatformProvisionAttempts:           true,
	cloudexport.MetricPlatformProvisionFailures:           true,
	cloudexport.MetricPlatformProvisionDurationSecondsSum: true,
}

// knownMetricTypes is the complete set of GCM metric types the
// cloudexport collector can emit that the dashboard may reference, derived
// from its exported instrument name consts (the same allowlist the
// collector's own golden tests pin).
func knownMetricTypes() map[string]bool {
	names := []string{
		cloudexport.MetricCPULoad1m,
		cloudexport.MetricCPULoad5m,
		cloudexport.MetricCPULoad15m,
		cloudexport.MetricMemoryUsed,
		cloudexport.MetricMemoryTotal,
		cloudexport.MetricDiskUsed,
		cloudexport.MetricDiskTotal,
		cloudexport.MetricContainerCount,
		cloudexport.MetricHeartbeat,
		cloudexport.MetricPlatformAPIRequests,
		cloudexport.MetricPlatformAPIErrors,
		cloudexport.MetricPlatformProvisionAttempts,
		cloudexport.MetricPlatformProvisionFailures,
		cloudexport.MetricPlatformProvisionDurationSecondsSum,
		cloudexport.MetricPlatformPeersConnected,
		cloudexport.MetricPlatformTunnelState,
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		kind := "gauge"
		if counterInstruments[n] {
			kind = "counter"
		}
		out["prometheus.googleapis.com/"+n+"/"+kind] = true
	}
	return out
}

// TestDashboardJSON_ValidAndReferencesKnownMetrics loads the committed
// dashboard and asserts (1) it is valid JSON gcloud can accept, and (2)
// every metric.type filter it contains names a series the collector
// actually exports — catching a typo or a renamed/removed instrument
// that a hand-edited dashboard JSON would otherwise drift from silently.
func TestDashboardJSON_ValidAndReferencesKnownMetrics(t *testing.T) {
	data, err := os.ReadFile("gcm-containarium-hosts.json")
	if err != nil {
		t.Fatalf("read dashboard JSON: %v", err)
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("dashboard JSON is invalid: %v", err)
	}

	if _, ok := doc["displayName"]; !ok {
		t.Error("dashboard JSON missing displayName")
	}

	known := knownMetricTypes()
	foundHost := false
	requiredPlatform := []string{cloudexport.MetricPlatformAPIErrors, cloudexport.MetricPlatformProvisionFailures, cloudexport.MetricPlatformPeersConnected}
	foundPlatform := map[string]bool{}
