package backup

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
)

// gateBox is a BoxOps whose export blocks until released, so a test can hold
// one backup in flight while it starts another. It is safe for concurrent
// use.
type gateBox struct {
	archive []byte
	usage   int64
	usages  map[string]int64 // per-box override of usage

	entered chan string   // receives the box name when an export starts
	release chan struct{} // closed to let every export proceed

	mu       sync.Mutex
	exported []string
}

func newGateBox(archive []byte, usage int64) *gateBox {
	return &gateBox{archive: archive, usage: usage, entered: make(chan string, 16), release: make(chan struct{})}
}

func (g *gateBox) ExportInstance(name string) (io.ReadCloser, error) {
	g.mu.Lock()
	g.exported = append(g.exported, name)
	g.mu.Unlock()
	g.entered <- name
	<-g.release
	return io.NopCloser(bytes.NewReader(g.archive)), nil
}

func (g *gateBox) BoxDiskUsage(name string) (int64, error) {
	if u, ok := g.usages[name]; ok {
		return u, nil
	}
	return g.usage, nil
}

func (g *gateBox) exports() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.exported...)
}

// recv waits for a result, failing instead of hanging when a backup that
// should have been refused is stuck in the gated export.
func recv(t *testing.T, out <-chan createResult) createResult {
	t.Helper()
	select {
	case r := <-out:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no result in 5s: a backup that should have been refused is running")
		return createResult{}
	}
}

// enter waits for a backup to reach its export, failing instead of hanging
// when one that should have been admitted was refused.
func enter(t *testing.T, gb *gateBox) {
	t.Helper()
	select {
	case <-gb.entered:
	case <-time.After(5 * time.Second):
