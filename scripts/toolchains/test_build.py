"""Behavioral tests for the release packager; all downloads are local fixtures."""
import hashlib
import importlib.util
import io
import json
import platform
from pathlib import Path
import tarfile
import tempfile
import subprocess
import unittest
from unittest import mock
import zipfile


class ToolchainBuildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        spec = importlib.util.spec_from_file_location("build", Path(__file__).with_name("build.py"))
        self.builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.builder)
        self.lock = {"tools": []}
        layouts = {"go": ["go/bin/go", "go/bin/gofmt"],
                   "node": ["node-v1/bin/node", "node-v1/bin/npm", "node-v1/bin/npx"],
                   "npm": ["package/bin/npm-cli.js", "package/bin/npx-cli.js"],
                   "pnpm": ["pnpm"], "staticcheck": ["staticcheck/staticcheck"]}
        for name in ("go", "node", "npm", "pnpm", "buf", "protoc", "staticcheck"):
            artifact = self.root / (name + ".download")
            if name == "buf":
                artifact.write_bytes(b"#!/bin/sh\necho fixture\n")
                kind = "binary"
            elif name == "protoc":
                with zipfile.ZipFile(artifact, "w") as archive:
                    archive.writestr("bin/protoc", "#!/bin/sh\necho fixture\n")
                    archive.writestr("include/google/protobuf/any.proto", "syntax='proto3';")
