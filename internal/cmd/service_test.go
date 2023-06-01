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
	}
	// If the path is also listed explicitly, it must be ignore-if-absent so the
	// entry itself can never fail namespace setup.
	if slices.Contains(rwFields, "/var/lib/containarium") {
		t.Errorf("ReadWritePaths lists /var/lib/containarium without the \"-\" ignore-if-absent prefix: %v", rwFields)
	}
}

// TestEnsureDaemonOwnedDirsCreatesThem covers the install-time half of the
// contract: these paths are Required in hostcheck.DaemonWritablePaths AND listed
// in the unit's ReadWritePaths, so if they are absent the doctor fails (aborting
// `pool join`, which runs its self-check after ensureDaemonUnitAndSecret) and
// systemd fails namespace setup with 226/NAMESPACE.
func TestEnsureDaemonOwnedDirsCreatesThem(t *testing.T) {
	root := t.TempDir()
	if err := ensureDaemonOwnedDirs(root); err != nil {
		t.Fatalf("ensureDaemonOwnedDirs: %v", err)
	}
	for _, dir := range daemonOwnedDirs {
		fi, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			t.Errorf("%s was not created: %v", dir, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s exists but is not a directory", dir)
		}
	}
	// `pool join` is documented idempotent, so re-running must not error.
	if err := ensureDaemonOwnedDirs(root); err != nil {
		t.Errorf("second call should be idempotent, got: %v", err)
	}
}

// TestDaemonOwnedDirsCoverContainariumOwnedWritablePaths is the join between the
// doctor contract and what we create. Any Required writable path under a
// Containarium-owned prefix must be in daemonOwnedDirs — otherwise the doctor
// demands a directory nothing creates, and `pool join` aborts on a fresh host.
// System paths (/etc, /home, /var/log, ...) are deliberately excluded: we must
// not create or re-mode those.
func TestDaemonOwnedDirsCoverContainariumOwnedWritablePaths(t *testing.T) {
	for _, required := range hostcheck.DaemonWritablePaths {
		if required != "/opt/containarium" && required != "/var/lib/containarium" {
			continue // inherited from the base system
		}
		if !slices.Contains(daemonOwnedDirs, required) {
			t.Errorf("hostcheck.DaemonWritablePaths requires %q but daemonOwnedDirs does not "+
				"create it; the doctor would fail and pool join would abort on a fresh host", required)
		}
	}
}

// TestDaemonUnitGrantsBackupStateDir guards the daemon's ability to write its
// backup state. pkg/core/backup MkdirAll+WriteFiles under
// /var/lib/containarium/backups on *every* destination (a GCS backup is staged
// locally first), plus the sidecar index, list, and restore temp files. That is
// the daemon process, under ProtectSystem=strict -- so without an explicit grant
// /var/lib is read-only and every backup fails EROFS. Nothing else on the host
// creates the directory, hence StateDirectory= rather than a bare path grant.
func TestDaemonUnitGrantsBackupStateDir(t *testing.T) {
	var stateDir string
	var rwFields []string
	for _, line := range strings.Split(systemdServiceTemplate, "\n") {
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
		t.Errorf("expected StateDirectory=containarium so systemd creates and grants "+
			"/var/lib/containarium for the backup service, got %q", stateDir)
	}
	granted := false
	for _, f := range rwFields {
		if strings.TrimPrefix(f, "-") == "/var/lib/containarium" {
			granted = true
			if !strings.HasPrefix(f, "-") {
				t.Errorf("/var/lib/containarium should be ignore-if-absent (\"-\" prefixed) in ReadWritePaths, got %q", f)
			}
		}
	}
	if !granted {
		t.Errorf("ReadWritePaths does not mention /var/lib/containarium: %v", rwFields)
	}
}

// TestOptContainariumIsIgnoreIfAbsent pins the "-" prefix on the one
// ReadWritePaths entry Containarium owns rather than inherits.
