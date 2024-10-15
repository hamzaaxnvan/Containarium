//go:build !windows && !containarium_client

package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/hostcheck"
)

// TestBackupUnitStateDirectory covers the sibling unit shipped as a plain file
// (scripts/containarium-backup.service) rather than generated from a Go
// template, so nothing else in the build would notice it regressing.
//
// That unit runs ProtectSystem=strict and needs /var/lib/containarium writable
// for the backup JSON index. Nothing on the host creates that directory --
// hacks/install.sh declares DATA_DIR=/var/lib/containarium but never creates it,
// and the one `mkdir -p /var/lib/containarium` in the tree runs inside a
// container via incus exec. StateDirectory= is what makes systemd create it and
// grant write access; an ignore-if-absent ReadWritePaths entry alone would let
// the unit start with the path read-only, so the index could not be written.
func TestBackupUnitStateDirectory(t *testing.T) {
	// Relative to this package dir; the unit ships in the repo, not embedded.
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "containarium-backup.service"))
	if err != nil {
		t.Fatalf("read backup unit: %v", err)
	}
	var stateDir string
	var rwFields []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "StateDirectory="):
			stateDir = strings.TrimPrefix(line, "StateDirectory=")
		case strings.HasPrefix(line, "ReadWritePaths="):
			rwFields = strings.Fields(strings.TrimPrefix(line, "ReadWritePaths="))
		}
	}
	if stateDir != "containarium" {
		t.Errorf("expected StateDirectory=containarium (creates + grants /var/lib/containarium), got %q", stateDir)
