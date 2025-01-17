"""containarium-instrument console script.

Thin alias over `opentelemetry-instrument` (decision D8 — always
installed). Adds:

  - `--dry-run`: prints resolved config and exits without launching
    the app (D12).
  - `--version` / `--help`: branding + usage.

Anything else execvp()s `opentelemetry-instrument` with the same args
and lets the upstream runtime drive the auto-instrument flow. Our
Distro + Configurator are registered as opentelemetry entry points, so
the upstream `opentelemetry-instrument` picks up our customizations
without us forking its argv parser.
"""
from __future__ import annotations

import os
