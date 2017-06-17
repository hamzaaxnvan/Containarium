package k8s

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const nsChartDir = "../../../../charts/containarium-k8s"

// nsRender renders the chart with the walkthrough's flags (jwt secret, upstream
// key secret and public key) plus extra args, and returns the manifest.
func nsRender(t *testing.T, chart string, extra ...string) string {
	t.Helper()
	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not found on PATH, skipping chart-rendering test")
