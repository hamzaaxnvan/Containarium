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
