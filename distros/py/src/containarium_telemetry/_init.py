"""Distro init — the public entry point.

Contract (per docs/TELEMETRY-DISTRO-DESIGN.md):
- Always fail-open. Missing endpoint → WARN + no-op handle (D10).
- Idempotent. Repeat calls log at DEBUG and return the existing handle.
- The OTLP exporters read OTEL_EXPORTER_OTLP_{ENDPOINT,HEADERS,
  PROTOCOL} from env directly, so we do not pass them as constructor
  args — that would shadow user overrides we're meant to honor.

Transport selection (#386): OTEL_EXPORTER_OTLP_PROTOCOL picks the
exporter. `grpc` uses the gRPC exporters (OTLP :4317); anything else
(default `http/protobuf`) uses the HTTP exporters (OTLP :4318). The
gRPC exporter package is an optional `grpc` extra and is imported
lazily, so HTTP-only installs are unaffected and a missing gRPC
package fails open rather than crashing the app at import time.
"""
from __future__ import annotations

import logging
import os
from typing import Dict, Optional

from opentelemetry import metrics, trace
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor

from ._config import DistroConfig
from ._distro import AUTO_INSTRUMENT_ENV_KEY
from ._instrumentations import InstrumentationsArg, register_instrumentations
from ._resource import build_resource

logger = logging.getLogger("containarium_telemetry")

_initialized: bool = False
_shutdown_handle: Optional["Shutdown"] = None


class Shutdown:
    """Idempotent shutdown handle returned by init().

    Holds whatever providers init() actually installed — the meter
    provider, and (when traces are exported) the tracer provider — and
    shuts both down. Either may be None (e.g. the fail-open no-op handle,
    or when the app already had its own tracer provider that we left
    untouched).
    """

    def __init__(
        self,
        provider: Optional[MeterProvider],
        tracer_provider: Optional[TracerProvider] = None,
    ):
        self._provider = provider
        self._tracer_provider = tracer_provider
        self._done = False

    def shutdown(self, timeout_s: float = 5.0) -> None:
        if self._done:
            return
        self._done = True
        if self._provider is not None:
            try:
                self._provider.shutdown(timeout_millis=int(timeout_s * 1000))
            except Exception as e:  # noqa: BLE001 — never raise from shutdown
                logger.warning("containarium_telemetry meter shutdown failed: %s", e)
        if self._tracer_provider is not None:
            try:
                # TracerProvider.shutdown() flushes the BatchSpanProcessor
                # and stops its worker thread. It takes no timeout arg.
                self._tracer_provider.shutdown()
            except Exception as e:  # noqa: BLE001 — never raise from shutdown
                logger.warning("containarium_telemetry tracer shutdown failed: %s", e)

    def __call__(self, timeout_s: float = 5.0) -> None:
        # Lets callers use the handle directly as `handle()` instead of
        # `handle.shutdown()` — convenient with atexit.register().
        self.shutdown(timeout_s)


def init(
    service_name: Optional[str] = None,
    extra_attrs: Optional[Dict[str, str]] = None,
    instrumentations: InstrumentationsArg = "auto",
    metric_export_interval_ms: int = 5_000,
    metric_export_timeout_ms: int = 10_000,
) -> Shutdown:
    """Initialize the distro. Returns an idempotent Shutdown handle.

    Args:
        service_name: Override OTEL_SERVICE_NAME if not already set.
        extra_attrs: Extra resource attributes — win over env attrs
            (precedence #5 in TELEMETRY-DISTRO-DESIGN.md).
        instrumentations: "auto" (default — every installed
            opentelemetry_instrumentor), "off", or a list of names.
            Skipped when invoked from `containarium-instrument` /
            `opentelemetry-instrument` (the runtime handles it).
        metric_export_interval_ms: Periodic export tick. Default 5s,
            matching the sidecar's batch processor.
        metric_export_timeout_ms: Per-export timeout. Default 10s.

    Fail-open: missing OTEL_EXPORTER_OTLP_ENDPOINT logs WARN and returns
    a no-op handle. The app never crashes because telemetry isn't wired.
    """
    global _initialized, _shutdown_handle

    if _initialized:
        logger.debug("init() called twice — returning existing handle")
        return _shutdown_handle  # type: ignore[return-value]
