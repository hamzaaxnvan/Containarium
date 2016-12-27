import os
from unittest import mock

import pytest

from containarium_telemetry import Shutdown, init
from containarium_telemetry._init import _reset_for_tests


@pytest.fixture(autouse=True)
def _reset():
    """Reset module-level init state before each test."""
    _reset_for_tests()
    yield
    _reset_for_tests()


def test_init_fail_open_without_endpoint(caplog):
    # No OTEL_EXPORTER_OTLP_ENDPOINT — distro should log WARN and
    # return a no-op handle. App keeps running.
    with mock.patch.dict(os.environ, {}, clear=True):
        with caplog.at_level("WARNING", logger="containarium_telemetry"):
            handle = init(instrumentations="off")
    assert isinstance(handle, Shutdown)
    assert any("OTEL_EXPORTER_OTLP_ENDPOINT" in r.message for r in caplog.records)
    # No-op shutdown shouldn't raise.
    handle.shutdown()


def test_init_with_endpoint_returns_shutdown():
    env = {
        "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
        "OTEL_SERVICE_NAME": "test-svc",
        "CONTAINARIUM_CONTAINER_ID": "alice-container",
        "CONTAINARIUM_BACKEND_ID": "node-7",
        "CONTAINARIUM_TENANT_ID": "alice",
    }
    with mock.patch.dict(os.environ, env, clear=True):
        handle = init()
    assert isinstance(handle, Shutdown)
    handle.shutdown()


def test_init_idempotent():
    env = {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318"}
    with mock.patch.dict(os.environ, env, clear=True):
