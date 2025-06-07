"""Auto-instrumentation registration via OTel entry points.

Used by the direct-API path: when a user calls
`containarium_telemetry.init(instrumentations="auto")`, we iterate every
package that registered an opentelemetry_instrumentor entry point and
call its `.instrument()` method. Failures from individual instrumentors
are logged at WARN and skipped — one broken integration doesn't abort
init().

The auto-instrument shim path (containarium-instrument / opentelemetry-
instrument) does NOT go through here — the upstream runtime registers
instrumentors itself.
"""
from __future__ import annotations

import logging
import sys
from importlib.metadata import entry_points
from typing import List, Union

logger = logging.getLogger("containarium_telemetry")

InstrumentationsArg = Union[str, List[str]]
