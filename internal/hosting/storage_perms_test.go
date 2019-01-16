package hosting

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Phase 4.8 — CheckStorageDirPerms (audit C-MED-7). TLS private
// keys live under the Caddy storage path; world-readable bits
// have no business there.

func TestCheckStorageDirPerms_Accepts0750(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file mode bits not meaningful on windows")
	}
	dir := filepath.Join(t.TempDir(), "caddy")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := CheckStorageDirPerms(dir); err != nil {
		t.Fatalf("0750 should pass: %v", err)
	}
}

func TestCheckStorageDirPerms_Accepts0700(t *testing.T) {
