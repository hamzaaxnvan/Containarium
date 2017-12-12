package sentinel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The live harness controls a VM. Exercise its success and failure paths
// against synthetic command-line services before giving it real credentials.
func TestSentinelOutageHarness(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"recover", "PASS"},
		{"slow-boot", "PASS"},
		{"bad-health-baseline", "backend health baseline failed"},
		{"boot-stuck", "backend did not become ready"},
		{"restart", "sentinel restarted"},
		{"restart-count", "sentinel restarted"},
		{"changed-ip", "backend IP changed"},
		{"ssh-stuck", "forwarding did not recover"},
