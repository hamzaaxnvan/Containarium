package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	deploySource    string
	deployPort      int32
	deploySubdomain string
	deployEnvVars   []string
	deployUsername  string
)

var appDeployCmd = &cobra.Command{
	Use:   "deploy <app-name>",
	Short: "Deploy an application",
	Long: `Deploy an application to your container.

The source directory should contain either:
  - A Dockerfile (recommended for custom builds)
  - Project files for auto-detection (package.json, requirements.txt, go.mod, etc.)

Supported languages for auto-detection:
  - Node.js (package.json)
  - Python (requirements.txt, Pipfile, pyproject.toml)
  - Go (go.mod)
  - Rust (Cargo.toml)
  - Ruby (Gemfile)
  - PHP (composer.json)
  - Static sites (index.html)

Examples:
  # Deploy from current directory
  containarium app deploy myapp --source .

  # Deploy with custom port
  containarium app deploy myapp --source ./app --port 8080

  # Deploy with environment variables
  containarium app deploy myapp --source . --env "DB_HOST=localhost" --env "DEBUG=true"

  # Deploy with custom subdomain
  containarium app deploy myapp --source . --subdomain my-custom-app`,
	Args: cobra.ExactArgs(1),
	RunE: runAppDeploy,
}

func init() {
	appCmd.AddCommand(appDeployCmd)

	appDeployCmd.Flags().StringVarP(&deploySource, "source", "s", ".", "Source directory to deploy")
	appDeployCmd.Flags().Int32VarP(&deployPort, "port", "p", 3000, "Application port")
	appDeployCmd.Flags().StringVar(&deploySubdomain, "subdomain", "", "Custom subdomain (default: username-appname)")
	appDeployCmd.Flags().StringArrayVarP(&deployEnvVars, "env", "e", nil, "Environment variables (KEY=VALUE)")
	appDeployCmd.Flags().StringVarP(&deployUsername, "user", "u", "", "Username (required for remote deployment)")
}

func runAppDeploy(cmd *cobra.Command, args []string) error {
	appName := args[0]

	// Validate app name
	if appName == "" {
		return fmt.Errorf("app name is required")
	}

	// For remote server, username is required
	if serverAddr != "" && deployUsername == "" {
		return fmt.Errorf("--user is required when deploying to a remote server")
