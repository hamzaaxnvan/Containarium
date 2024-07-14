package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Phase 4.2 — master key file must be 0400 (or stricter) at load
// time. Audit finding C-HIGH-6: the file is created at 0400 but
// umask drift, ownership change, or backup-tool side effects can
// widen the permissions silently. LoadOrCreateMasterKey must
// fail-closed if any non-owner bit is set.

func TestLoadOrCreateMasterKey_RejectsWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file mode bits not meaningful on windows")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "master.key")
	if err := os.WriteFile(path, make([]byte, MasterKeySize), 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, err := LoadOrCreateMasterKey(path)
	if err == nil {
		t.Fatal("world-readable master key must be rejected")
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("error should mention permissions: %v", err)
	}
}

func TestLoadOrCreateMasterKey_RejectsGroupReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file mode bits not meaningful on windows")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "master.key")
	if err := os.WriteFile(path, make([]byte, MasterKeySize), 0o440); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, err := LoadOrCreateMasterKey(path)
	if err == nil {
		t.Fatal("group-readable master key must be rejected")
	}
}

func TestLoadOrCreateMasterKey_AcceptsMode0400(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file mode bits not meaningful on windows")
	}
