// Package cloudexport is the seam for opt-in export of Containarium's
// host/container infra metrics to a host cloud's native monitoring
// (GCP Cloud Monitoring first — #1069/#1070/#1071).
//
// #1069 delivers the toggle (enable/disable/status), typed config
// persistence, and the enable-time credential probe. The full collector
// (CloudExportCollector + Sources + the allowlisted OTel instrument set
// described in docs/CLOUD-NATIVE-METRICS-EXPORT-DESIGN.md) lands with
// #1070 (host series) and #1071 (container series); this package is the
// skeleton those land into.
package cloudexport

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
