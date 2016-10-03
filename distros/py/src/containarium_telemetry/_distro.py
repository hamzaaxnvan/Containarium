"""OTel Distro + Configurator entry points for the auto-instrument path.

When the user runs `containarium-instrument python app.py` (or the
upstream `opentelemetry-instrument` with this package installed), the
OTel runtime loads:

  1. ContainariumDistro._configure()  — sets exporter env defaults
  2. ContainariumConfigurator._configure() — runs our init()
  3. Then iterates registered opentelemetry_instrumentor entry points
     and calls .instrument() on each.

Our init() detects the auto-instrument context via the
_CONTAINARIUM_TELEMETRY_AUTO_INSTRUMENT env sentinel and skips its own
instrumentation-registration step so we don't double-instrument.
"""
from __future__ import annotations
