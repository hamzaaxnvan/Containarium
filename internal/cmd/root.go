package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/footprintai/containarium/internal/releases"
	"github.com/footprintai/containarium/pkg/version"
	"github.com/spf13/cobra"
)

// explicitServerAddr is the server named by --server or CONTAINARIUM_SERVER
// for this invocation ("" when only a login's default_server, or nothing,
// supplied one).
var explicitServerAddr string

var (
	cfgFile        string
	verbose        bool
	versionCheck   bool
	serverAddr     string
	certsDir       string
	insecure       bool
	verboseVersion bool
	// HTTP mode flags
	httpMode  bool
	authToken string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "containarium",
	Short: "Containarium - SSH Jump Server + LXC Container Platform",
	Long: `Containarium is a production-ready platform for providing isolated
development environments using LXC containers on a single cloud VM.

It enables you to:
  - Create isolated Ubuntu containers with Docker support
  - Manage SSH access for multiple users
  - Deploy infrastructure with Terraform
  - Efficiently utilize cloud resources (10x savings vs VM-per-user)

Examples:
  # Create a new container for user alice
  containarium create alice --ssh-key ~/.ssh/alice.pub

  # List all containers
  containarium list

  # Delete a container
  containarium delete alice

  # Show system information
  containarium info`,
	// PersistentPreRunE resolves the effective server address and fills in
	// the auth token from the credentials file when neither --token nor
	// CONTAINARIUM_TOKEN are set.
	//
	// Server precedence (see resolveServerAddr):
	//   1. --server flag (explicit)
	//   2. CONTAINARIUM_SERVER env var (becomes the flag default in init()
	//      below — so by the time this runs, both collapse into serverAddr)
	//   3. ~/.containarium/credentials.json default_server — client build
	//      only (#1776); containariumd never picks this up, so an
	//      operator's host can't be silently repointed from local Incus to
	//      a stale remote fleet.
	//
	// Token precedence:
	//   1. --token flag (explicit)
	//   2. CONTAINARIUM_TOKEN env var (becomes the flag default
	//      in init() below — so by the time this runs, both
	//      collapse into authToken)
	//   3. ~/.containarium/credentials.json (server matching
	//      --server, else default_server)
	// See `containarium login --help`.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// login / logout / whoami / config get-token are the
		// commands that produce/consume the credentials file
		// directly. Skip the auto-fill for them — login is
		// unauthenticated by definition, and we don't want a
		// stale token (or server) to leak into a logout call.
		switch cmd {
		case loginCmd, logoutCmd, whoamiCmd, configGetTokenCmd:
			return nil
		}
		// Recorded before resolution: --server or CONTAINARIUM_SERVER, never
		// the credentials file's default_server. `guardrail apply/verify`
		// contact a server only when it was named this way (#2368).
