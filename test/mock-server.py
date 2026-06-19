#!/usr/bin/env python3
"""
Mock GitHub API server for testing upgrade commands locally.

Usage:
    python3 test/mock-server.py

Then in another terminal:
    ./bin/containarium upgrade self --check --test-url http://localhost:8080/release.json
"""

import http.server
import socketserver
import os
import json
from pathlib import Path

PORT = 8080

class MockGitHubHandler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        # Serve the mock release JSON
        if self.path == '/release.json':
            fixtures_dir = Path(__file__).parent / 'fixtures'
            release_file = fixtures_dir / 'mock-release.json'

