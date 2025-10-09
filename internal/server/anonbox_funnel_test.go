package server

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/events"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func funnelTestSink(t *testing.T) (*anonFunnelSink, *sdkmetric.ManualReader, *events.Subscriber) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	bus := events.NewBus()
	sub := bus.Subscribe(&pb.SubscribeEventsRequest{})
	t.Cleanup(func() { bus.Unsubscribe(sub.ID) })
	s, err := newAnonFunnelSink(bus, mp)
	if err != nil {
		t.Fatal(err)
	}
	return s, reader, sub
}

func counterValues(t *testing.T, reader *sdkmetric.ManualReader) (map[string]int64, map[string]uint64) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
