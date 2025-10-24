import os
from unittest import mock

from containarium_telemetry._config import DistroConfig
from containarium_telemetry._resource import (
    CONTAINARIUM_DISTRO_ATTR_KEY,
    build_resource,
)


def _config_with(**overrides):
    """Build a DistroConfig with only the fields named."""
    base = dict(
        endpoint=None,
        service_name=None,
        resource_attributes=None,
        headers=None,
        protocol=None,
        container_id=None,
        backend_id=None,
        tenant_id=None,
        service_version=None,
    )
    base.update(overrides)
    return DistroConfig(**base)
