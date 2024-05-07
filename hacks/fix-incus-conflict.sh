#!/bin/bash
# Fix Incus package conflict between Ubuntu and Zabbly repositories
#
# This script resolves the common issue where Ubuntu's default Incus (6.0.0)
# conflicts with Zabbly's newer version (6.19+)

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

