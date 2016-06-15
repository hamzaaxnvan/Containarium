// Package containariumotel is the Go telemetry distro for apps and
// services running on the Containarium platform.
//
// It pairs with the daemon's per-LXC env-stamping (--monitoring=true)
// and the central otel-collector / otel-sidecar pair: callers get a
// MeterProvider wired with the resource attrs Containarium expects
// (container.id, backend.id, tenant.id, service.version) plus the
// defended containarium.distro stamp, and an OTLP/HTTP exporter
// pointed at whatever endpoint the env sets.
//
// Basic usage:
//
//	shutdown, err := containariumotel.Init(ctx)
//	if err != nil {
