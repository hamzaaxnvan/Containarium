"""Env-driven configuration for the distro.

The dataclass exists so tests can construct it directly without
monkey-patching os.environ — every other module in the distro reads
from a DistroConfig, never from os.getenv().
"""
from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Optional


@dataclass(frozen=True)
class DistroConfig:
    # OTel-standard env vars (the SDK also reads these directly; we
    # capture them here for diagnostics and for code paths that need
    # to branch on their presence).
