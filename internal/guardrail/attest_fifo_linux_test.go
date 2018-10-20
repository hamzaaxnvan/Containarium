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
