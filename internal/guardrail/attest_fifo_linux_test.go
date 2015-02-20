//go:build linux

package guardrail

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// exchangeLoop atomically swaps a and b (renameat2 RENAME_EXCHANGE) until
// stop is closed — the same race Stage's FIFO test exercises.
func exchangeLoop(t *testing.T, a, b string, stop <-chan struct{}, wg *sync.WaitGroup) {
	t.Helper()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE); err != nil {
				t.Errorf("renameat2: %v", err)
				return
			}
		}
	}()
}

// TestSubjectDigest_RaceSwappingAFileForAFIFONeverHangs: a regular file
// swapped for a FIFO between the walk's type check and the open must not
// block SubjectDigest (#2480).
func TestSubjectDigest_RaceSwappingAFileForAFIFONeverHangs(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o644))
	if err := unix.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
