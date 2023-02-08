//go:build !containarium_client

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/hosting"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	// Setup flags
	setupDomain    string
	setupEmail     string
	setupProvider  string
	setupAPIKey    string
	setupAPISecret string
	setupServerIP  string
	skipDNS        bool
	skipCaddy      bool
	skipSaveConfig bool
	configFile     string
	noWildcard     bool
)

var hostingSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up DNS and SSL for app hosting",
	Long: `Automatically configure DNS records and SSL certificates for app hosting.

This command performs the following steps:
  1. Verifies DNS provider API credentials
  2. Verifies domain ownership in your DNS provider account
  3. Detects your server's public IP address (or uses --server-ip)
  4. Creates DNS records (A record for @ and * pointing to server IP)
  5. Installs Caddy with DNS provider plugin
  6. Configures Caddy for automatic Let's Encrypt SSL via DNS-01 challenge
  7. Starts Caddy service

Prerequisites:
  - Root/sudo access (for Caddy installation and systemd)
  - Go 1.21+ (for building Caddy with DNS plugin)
  - DNS provider API credentials

API Credentials:
  Credentials can be provided via (in priority order):
  1. Command line flags: --api-key and --api-secret
  2. Environment variables: GODADDY_API_KEY and GODADDY_API_SECRET
  3. Config file: /etc/containarium/hosting.json
  4. Interactive prompt (if not provided)

  After successful setup, credentials are saved to /etc/containarium/hosting.json
  with restricted permissions (0600). Use --no-save to skip saving.

Examples:
  # Interactive setup (prompts for credentials)
  sudo containarium hosting setup --domain example.com --email admin@example.com

  # Non-interactive with flags
  sudo containarium hosting setup \
    --domain example.com \
    --email admin@example.com \
    --provider godaddy \
    --api-key YOUR_KEY \
    --api-secret YOUR_SECRET

  # Using environment variables
  export GODADDY_API_KEY=your-key
  export GODADDY_API_SECRET=your-secret
  sudo containarium hosting setup --domain example.com --email admin@example.com

  # Skip DNS setup (if records already exist)
  sudo containarium hosting setup --domain example.com --email admin@example.com --skip-dns
