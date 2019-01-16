//go:build !containarium_client

package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

var statusDomain string

var hostingStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check hosting infrastructure status",
	Long: `Check the status of the hosting infrastructure including:
  - Caddy service status
  - DNS resolution
  - HTTPS certificate validity

Examples:
  # Check status for a domain
  containarium hosting status --domain example.com

  # Check status without domain verification
  containarium hosting status`,
	RunE: runHostingStatus,
}

func init() {
	hostingCmd.AddCommand(hostingStatusCmd)
	hostingStatusCmd.Flags().StringVar(&statusDomain, "domain", "", "Domain to check (optional)")
}

func runHostingStatus(cmd *cobra.Command, args []string) error {
	fmt.Println("Hosting Infrastructure Status")
	fmt.Println("==============================")
	fmt.Println()

	// Check Caddy installation
	fmt.Print("Caddy binary:     ")
	if _, err := os.Stat("/usr/local/bin/caddy"); err == nil {
		fmt.Println("installed (/usr/local/bin/caddy)")
	} else {
		fmt.Println("not installed")
	}

	// Check Caddy service status
	fmt.Print("Caddy service:    ")
	caddyStatus := exec.Command("systemctl", "is-active", "--quiet", "caddy")
	if err := caddyStatus.Run(); err == nil {
		fmt.Println("running")
	} else {
		fmt.Println("not running")
	}

	// Check Caddy Admin API
	fmt.Print("Caddy Admin API:  ")
	client := &http.Client{Timeout: 5 * time.Second}
	if resp, err := client.Get("http://localhost:2019/config/"); err == nil {
		resp.Body.Close()
