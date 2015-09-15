package hypervisor

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortTempDir returns a short-path temp directory. Unlike t.TempDir()
// (which embeds the full test name), this stays under the ~104-byte
// sun_path limit UNIX domain sockets have on macOS/BSD.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hv")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
