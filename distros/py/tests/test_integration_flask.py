"""Integration test: Flask gets auto-instrumented via the [flask] extra.

Verifies the end-to-end auto-instrumentation contract without spinning
up a real HTTP server — we install the FlaskInstrumentor via init(),
then ask it whether OpenTelemetry's global instrumented-state flag is
set on the Flask class.
"""
import os
from unittest import mock

import pytest

pytest.importorskip("flask")
pytest.importorskip("opentelemetry.instrumentation.flask")

