"""Pretty-printer for the resolved telemetry config.

Used by `containarium-instrument --dry-run` (D12). Reports endpoint,
protocol, resource attrs, headers (bearer redacted by default), and
whether the pipeline would actually wire up given the env.
"""
from __future__ import annotations

from typing import Optional

from ._config import DistroConfig
from ._version import __version__

_REDACTED_HEADER_KEYS = frozenset({"authorization", "x-api-key"})


def format_config(config: DistroConfig, *, redact_bearer: bool = True) -> str:
    """Return a human-readable rendering of the resolved config."""
    lines = [
        "# containarium-telemetry resolved config",
        f"distro_version       : {__version__}",
        f"endpoint             : {config.endpoint or '<unset>'}",
        f"protocol             : {config.protocol or '<default: http/protobuf>'}",
        f"service_name         : {config.service_name or '<unset — SDK will default to unknown_service>'}",
        f"resource_attributes  : {config.resource_attributes or '<unset>'}",
