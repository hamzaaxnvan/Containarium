#!/usr/bin/env python3
"""Verify downloaded release assets and their published Sigstore bundles (#2561)."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile


def verify_assets(directory, metadata, repository, tag, *, runner=None):
    """Fail closed on incomplete inventory, invalid signatures or mismatched data."""
    directory = Path(directory)
    runner = runner or subprocess.run
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", tag):
        raise ValueError("tag must be an exact vMAJOR.MINOR.PATCH release tag")
    if metadata.get("tag_name") != tag:
        raise ValueError("downloaded release metadata does not match the requested tag")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("repository must be owner/name, preserving its exact case")
    version = tag[1:]
    chart = f"containarium-k8s-{version}.tgz"
    bundle = f"containarium-bundle-{tag}-linux-amd64.tar.gz"
    binaries = {p + "-linux-amd64" for p in ("containariumd", "containarium", "mcp-server", "agent-box")}
    sboms = {p + ".spdx.json" for p in binaries}
    required = binaries | sboms | {
        "LICENSE", "NOTICE", "SHA256SUMS.txt", "agent-runtime-bundle.tar.gz",
        "sdlc-skills.tar.gz", "sdlc-skills.tar.gz.sha256", chart, chart + ".sha256",
        bundle, bundle + ".sha256",
    }
    advertised = [a["name"] for a in metadata["assets"]]
    if len(set(advertised)) != len(advertised) or any(
        Path(name).name != name or name in (".", "..") for name in advertised
    ):
        raise ValueError("invalid release inventory names")
    local = {p.name for p in directory.iterdir()}
    if set(advertised) != local:
        raise ValueError("download inventory differs from the published release")
    missing = required - local
    if missing:
        raise ValueError(f"missing required release assets: {sorted(missing)}")
    if any(p.is_symlink() or not p.is_file() for p in directory.iterdir()):
