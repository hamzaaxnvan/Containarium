package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/footprintai/containarium/pkg/core/container"
)

// NewCreateStageObserver builds the container.StageObserver that records
// per-stage create latency into the histogram
// containarium.container.create_stage_duration_seconds, labeled by stage.
//
// This is the Phase 0 instrument of
// docs/architecture/two-digit-ms-sandbox-spawn.md: it replaces that note's
// code-derived cost table with measured numbers, and any later create-path
// regression names its stage instead of a total.
func NewCreateStageObserver(provider *sdkmetric.MeterProvider) (container.StageObserver, error) {
	meter := provider.Meter("containarium.container")

	// Buckets span the path's real dynamic range: get_info at ~10ms up to an
	// unbaked install_packages at minutes.
	hist, err := meter.Float64Histogram("containarium.container.create_stage_duration_seconds",
		otelmetric.WithDescription("Duration of each stage of container create"),
		otelmetric.WithUnit("s"),
