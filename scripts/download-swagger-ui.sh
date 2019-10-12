#!/bin/bash
set -e

SWAGGER_VERSION="5.11.0"
SWAGGER_DIR="internal/gateway/swagger-ui"

echo "==> Downloading Swagger UI v${SWAGGER_VERSION}..."

# Create temporary directory
TEMP_DIR=$(mktemp -d)
