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
