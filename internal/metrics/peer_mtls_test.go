package metrics

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func peerMTLSValue(t *testing.T, enabled, relevant, set bool) (int64, bool) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	c := &Collector{
		config:   CollectorConfig{LocalBackendID: "b1"},
		provider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		ctx:      context.Background(),
	}
	if err := c.initInstruments(); err != nil {
		t.Fatal(err)
	}
	if set {
		c.SetPeerMTLSFunc(func() (bool, bool) { return enabled, relevant })
	}
	c.recordPeerMTLS()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
