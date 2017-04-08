from containarium_telemetry._config import DistroConfig


def test_from_env_empty():
    cfg = DistroConfig.from_env(env={})
    assert cfg.endpoint is None
    assert cfg.service_name is None
    assert cfg.container_id is None
    assert cfg.backend_id is None
    assert cfg.tenant_id is None
    assert cfg.service_version is None
    assert cfg.resource_attributes is None
    assert cfg.headers is None
    assert cfg.protocol is None


def test_from_env_populated():
    env = {
        "OTEL_EXPORTER_OTLP_ENDPOINT": "http://10.0.3.42:4318",
        "OTEL_SERVICE_NAME": "payment-api",
        "OTEL_RESOURCE_ATTRIBUTES": "container.id=alice,backend.id=node-7",
        "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer abc",
        "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
        "CONTAINARIUM_CONTAINER_ID": "alice-container",
