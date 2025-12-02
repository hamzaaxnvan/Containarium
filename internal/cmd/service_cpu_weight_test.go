//go:build !windows && !containarium_client

package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/hostcheck"
)

// unitDirective returns the value of the LAST non-comment `key=` line in a
// unit's text ("" if absent) — systemd's last-wins semantics.
func unitDirective(unit, key string) string {
	val := ""
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, key+"=") {
			val = strings.TrimPrefix(line, key+"=")
		}
	}
	return val
}

// TestSystemdServiceCPUWeight: the generated daemon unit carries the
// platform CPU weight the doctor checks for (#2284), so `service install`
// alone protects the daemon — no separate drop-in to forget.
func TestSystemdServiceCPUWeight(t *testing.T) {
	want := strconv.Itoa(hostcheck.PlatformCPUWeight)
	if got := unitDirective(systemdServiceTemplate, "CPUWeight"); got != want {
		t.Errorf("systemdServiceTemplate CPUWeight=%q, want %q (hostcheck.PlatformCPUWeight)", got, want)
	}
	if got := unitDirective(systemdServiceTemplate, "CPUAccounting"); got != "yes" {
