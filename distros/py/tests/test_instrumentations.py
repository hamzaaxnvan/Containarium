from unittest import mock

from containarium_telemetry._instrumentations import register_instrumentations


def _make_entry(name: str, instrumentor_mock=None, load_raises=None):
    ep = mock.MagicMock()
    ep.name = name
    if load_raises is not None:
        ep.load.side_effect = load_raises
    else:
        # entry.load() returns the instrumentor *class*. instrumentor() is
        # the call to instantiate, .instrument() registers it.
        instrumentor_class = mock.MagicMock(return_value=instrumentor_mock)
        ep.load.return_value = instrumentor_class
    return ep


def test_off_returns_empty():
    assert register_instrumentations("off") == []


def test_unrecognized_returns_empty():
    assert register_instrumentations("garbage") == []


