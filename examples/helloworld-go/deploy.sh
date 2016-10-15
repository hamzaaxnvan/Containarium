#!/usr/bin/env bash
# Runs inside the container as the post-receive deploy hook (configured
# via mcp__push --deploy_cmd "bash deploy.sh"). Four jobs:
#   1. Build the Go binary in-place (assumes the container image has
#      a Go toolchain — typical for an LXC bootstrapped with golang).
