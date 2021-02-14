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


@pytest.fixture(autouse=True)
def _reset():
    _reset_for_tests()
    yield
    _reset_for_tests()


def _cfg(protocol):
    return DistroConfig(
        endpoint="http://127.0.0.1:4318",
        service_name=None,
        resource_attributes=None,
        headers=None,
        protocol=protocol,
        container_id=None,
        backend_id=None,
        tenant_id=None,
