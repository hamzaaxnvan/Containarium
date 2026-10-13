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

            if release_file.exists():
                self.send_response(200)
                self.send_header('Content-type', 'application/json')
                self.end_headers()

                with open(release_file, 'rb') as f:
                    self.wfile.write(f.read())
            else:
                self.send_error(404, 'Mock release file not found')

        # Serve mock binaries (empty files for testing)
        elif self.path.startswith('/binaries/'):
            binary_name = os.path.basename(self.path)

            self.send_response(200)
            self.send_header('Content-type', 'application/octet-stream')
            self.send_header('Content-Disposition', f'attachment; filename="{binary_name}"')
            self.end_headers()

            # Send a mock binary (just a simple script that prints version)
            mock_binary = b'#!/bin/bash\necho "Containarium v0.3.0 (mock)"\n'
            self.wfile.write(mock_binary)

        else:
            self.send_error(404, 'Not Found')

    def log_message(self, format, *args):
        # Custom log format
        print(f"[MockServer] {args[0]}")

def run_server():
    with socketserver.TCPServer(("", PORT), MockGitHubHandler) as httpd:
        print(f"🧪 Mock GitHub API Server")
        print(f"=" * 50)
        print(f"Listening on http://localhost:{PORT}")
        print(f"")
        print(f"Endpoints:")
        print(f"  GET /release.json     - Mock release info")
        print(f"  GET /binaries/*       - Mock binary downloads")
        print(f"")
        print(f"Test commands:")
        print(f"  ./bin/containarium upgrade self --check \\")
        print(f"    --test-url http://localhost:{PORT}/release.json")
        print(f"")
        print(f"Press Ctrl+C to stop")
        print(f"=" * 50)

        try:
            httpd.serve_forever()
        except KeyboardInterrupt:
            print(f"\n\nShutting down mock server...")

if __name__ == '__main__':
    run_server()
