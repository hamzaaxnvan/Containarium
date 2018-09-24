#!/bin/bash
set -e

SWAGGER_VERSION="5.11.0"
SWAGGER_DIR="internal/gateway/swagger-ui"

echo "==> Downloading Swagger UI v${SWAGGER_VERSION}..."

# Create temporary directory
TEMP_DIR=$(mktemp -d)
cd "$TEMP_DIR"

# Download Swagger UI
echo "==> Downloading from GitHub..."
curl -LO "https://github.com/swagger-api/swagger-ui/archive/refs/tags/v${SWAGGER_VERSION}.tar.gz"

# Extract
echo "==> Extracting..."
tar -xzf "v${SWAGGER_VERSION}.tar.gz"

