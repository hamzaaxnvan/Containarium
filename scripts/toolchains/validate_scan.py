#!/usr/bin/env python3
"""Refuse a source-only Trivy report for a deployed toolchain tree."""
import json
from pathlib import Path
import sys


def validate(report):
    binaries = {r["Target"] for r in report.get("Results", []) if r.get("Type") == "gobinary"}
    for path in ("go/bin/go", "bin/buf", "staticcheck/staticcheck"):
        if not any(target == path or target.endswith("/" + path) for target in binaries):
