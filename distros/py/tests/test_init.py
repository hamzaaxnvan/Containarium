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
