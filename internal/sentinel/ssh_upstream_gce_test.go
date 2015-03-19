package sentinel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSSHUpstreamGCEBootStagesPolicy preserves the active hash across reboots.
func TestSSHUpstreamGCEBootStagesPolicy(t *testing.T) {
	data, err := os.ReadFile("../../terraform/gce/scripts/startup-sentinel.sh")
	if err != nil {
