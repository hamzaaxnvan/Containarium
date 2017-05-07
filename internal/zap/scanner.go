package zap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// zapAPIPort is the port ZAP daemon listens on for its REST API
	zapAPIPort = 8090
)

// Scanner wraps the ZAP daemon running inside the security container
type Scanner struct {
	apiBase    string // set after discovering the container IP
	httpClient *http.Client
}

// NewScanner creates a new ZAP scanner
func NewScanner() *Scanner {
	return &Scanner{
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Available returns whether ZAP is installed in the security container
func (s *Scanner) Available() bool {
	installer := NewInstaller()
	return installer.IsInstalled()
}

// Version returns the ZAP version string
func (s *Scanner) Version() string {
	if s.apiBase == "" {
		return ""
	}
	resp, err := s.apiGet("/JSON/core/view/version/")
	if err != nil {
		return ""
	}
	if v, ok := resp["version"].(string); ok {
		return v
	}
	return ""
}

// getContainerIP returns the IP of the security container
func getContainerIP() (string, error) {
	cmd := exec.Command("incus", "list", SecurityContainerName, "--format=csv", "-c4")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get security container IP: %w", err)
	}
	// Output format: "10.0.3.8 (eth0)"
	ip := strings.TrimSpace(string(out))
	if idx := strings.Index(ip, " "); idx > 0 {
		ip = ip[:idx]
	}
	if ip == "" {
		return "", fmt.Errorf("security container has no IP address")
	}
	return ip, nil
}

// EnsureDaemonRunning starts the ZAP daemon in the security container if it's not already running
func (s *Scanner) EnsureDaemonRunning(ctx context.Context) error {
	// Fail fast if ZAP was never installed (InstallZap RPC never run on this
	// host). Without this check, every scan job silently backgrounds a
	// "command not found" inside the container and then burns the full
	// 120-second readiness poll before reporting the generic timeout below —
	// indistinguishable from a slow-starting daemon, and it repeats forever
	// since nothing here ever installs ZAP.
	if !s.Available() {
		return fmt.Errorf("ZAP is not installed in the security container (run the InstallZap RPC/API for this host first)")
	}

	// Get container IP
	ip, err := getContainerIP()
	if err != nil {
		return err
	}
	s.apiBase = fmt.Sprintf("http://%s:%d", ip, zapAPIPort)

	// Check if already running
	if s.isRunning() {
		return nil
	}

	log.Printf("ZAP: Starting daemon in security container on port %d", zapAPIPort)

	// Clean stale lock files from any previous crashed ZAP instance
	cleanCmd := exec.CommandContext(ctx, "incus", "exec", SecurityContainerName, "--",
		"bash", "-c", "rm -f /root/.ZAP/.homelock /root/.ZAP/session/*.lck /root/.ZAP/db/*.lck 2>/dev/null; pkill -f zap-2 2>/dev/null; true")
	_, _ = cleanCmd.CombinedOutput() // ignore errors

	// Start ZAP daemon inside the security container (background)
	// -host 0.0.0.0 makes ZAP listen on all interfaces so the host can reach it
	cmd := exec.CommandContext(ctx, "incus", "exec", SecurityContainerName, "--",
		"bash", "-c", fmt.Sprintf(
			"nohup %s/ZAP/zap.sh -daemon -host 0.0.0.0 -port %d -config api.disablekey=true -config api.addrs.addr.name=.* -config api.addrs.addr.regex=true > /var/log/zap.log 2>&1 &",
			zapInstallDir, zapAPIPort,
		))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start ZAP daemon: %w (%s)", err, string(out))
	}

	// Wait for ZAP to be ready (up to 120 seconds — ZAP is slow to start)
	for i := 0; i < 120; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if s.isRunning() {
			log.Printf("ZAP: Daemon ready at %s", s.apiBase)
			return nil
		}
	}

	return fmt.Errorf("ZAP daemon did not become ready within 120 seconds")
}

// isRunning checks if ZAP daemon is responding
func (s *Scanner) isRunning() bool {
	if s.apiBase == "" {
		return false
	}
	_, err := s.apiGet("/JSON/core/view/version/")
	return err == nil
}

// ScanURL performs a full spider + active scan on a URL and returns alerts
func (s *Scanner) ScanURL(ctx context.Context, targetURL string) ([]Alert, error) {
	// Step 1: Spider the URL to discover pages
	log.Printf("ZAP: Spidering %s", targetURL)
	spiderScanID, err := s.startSpider(targetURL)
	if err != nil {
		return nil, fmt.Errorf("failed to start spider: %w", err)
	}

	if err := s.waitForSpider(ctx, spiderScanID); err != nil {
		return nil, fmt.Errorf("spider failed: %w", err)
	}
	log.Printf("ZAP: Spider completed for %s", targetURL)

	// Step 2: Active scan the URL
	log.Printf("ZAP: Active scanning %s", targetURL)
	activeScanID, err := s.startActiveScan(targetURL)
	if err != nil {
		return nil, fmt.Errorf("failed to start active scan: %w", err)
	}

	if err := s.waitForActiveScan(ctx, activeScanID); err != nil {
		return nil, fmt.Errorf("active scan failed: %w", err)
	}
	log.Printf("ZAP: Active scan completed for %s", targetURL)

	// Step 3: Collect alerts for this URL
	return s.getAlerts(targetURL)
}

// GenerateHTMLReport generates an HTML report from ZAP
func (s *Scanner) GenerateHTMLReport() (string, error) {
	resp, err := s.apiGetRaw("/OTHER/core/other/htmlreport/")
	if err != nil {
		return "", fmt.Errorf("failed to generate HTML report: %w", err)
	}
	return resp, nil
}

// GenerateJSONReport generates a JSON report from ZAP
func (s *Scanner) GenerateJSONReport() (string, error) {
	resp, err := s.apiGetRaw("/OTHER/core/other/jsonreport/")
	if err != nil {
		return "", fmt.Errorf("failed to generate JSON report: %w", err)
	}
	return resp, nil
}

// startSpider initiates a ZAP spider scan
func (s *Scanner) startSpider(url string) (string, error) {
	resp, err := s.apiGet(fmt.Sprintf("/JSON/spider/action/scan/?url=%s&maxChildren=10&recurse=true&subtreeOnly=true", url))
	if err != nil {
		return "", err
	}
	scanID, ok := resp["scan"].(string)
	if !ok {
		if num, ok := resp["scan"].(float64); ok {
			scanID = strconv.Itoa(int(num))
		} else {
			return "", fmt.Errorf("unexpected spider response: %v", resp)
		}
	}
	return scanID, nil
}

// waitForSpider waits for a spider scan to complete
func (s *Scanner) waitForSpider(ctx context.Context, scanID string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}

		resp, err := s.apiGet(fmt.Sprintf("/JSON/spider/view/status/?scanId=%s", scanID))
		if err != nil {
			return err
		}
		statusStr, _ := resp["status"].(string)
		status, _ := strconv.Atoi(statusStr)
		if status >= 100 {
