"""Distro version string.

Resolved from the installed package metadata so there's a single source
of truth (`version =` in pyproject.toml). Falls back to a sentinel only
when the package isn't installed — e.g. running tests from a source
checkout without `pip install -e .`.
