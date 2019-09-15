package reqrate

import (
	"bufio"
	"io"
	"sync"
	"time"
)

// Counter accumulates per-host request counts for the current interval. The
// live tailer feeds it via Add from a single goroutine while the collector
// drains it via Snapshot on each tick; the mutex makes that hand-off safe.
type Counter struct {
	mu     sync.Mutex
	counts map[string]int64
}

// NewCounter returns an empty per-host counter.
func NewCounter() *Counter {
	return &Counter{counts: make(map[string]int64)}
}

// Add records one request against host.
func (c *Counter) Add(host string) {
	c.mu.Lock()
	c.counts[host]++
	c.mu.Unlock()
}

// Snapshot returns per-host requests-per-second over interval and resets the
// counter for the next window. It returns nil (and still resets) when there is
// nothing to report or interval is non-positive — the latter guards against a
// div-by-zero from a misconfigured tick.
func (c *Counter) Snapshot(interval time.Duration) map[string]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
