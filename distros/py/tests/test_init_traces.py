"""Tests for the traces pipeline + OTLP transport selection (#386)."""
import os
from unittest import mock

import pytest
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider as SDKTracerProvider

from containarium_telemetry import init
from containarium_telemetry._config import DistroConfig
from containarium_telemetry._init import (
    _make_metric_exporter,
    _make_span_exporter,
    _otlp_protocol,
    _reset_for_tests,
)

