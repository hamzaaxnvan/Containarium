//go:build !windows

package agentbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// DefaultStdioDrainGrace bounds how long agent-box keeps running after its
// MCP client closed stdin, waiting for requests it has already read to be
// answered. process_start and the file tools answer in milliseconds; the
// bound exists so a handler stuck behind a dead session cannot keep the
// process alive indefinitely.
const DefaultStdioDrainGrace = 30 * time.Second

// DrainingStdio wraps the MCP stdio pair so that EOF on stdin is withheld
// from the server until every request read through it has been answered
// on stdout (or the grace period elapses).
//
// Why: mcp-go's StdioServer queues tools/call requests to a worker pool and,
// when stdin hits EOF, cancels the request context BEFORE it closes the
// queue and waits for the workers. An idle worker's select then sees both
// "context done" and "work queued" and Go picks at random, so a request the
// client sent immediately before closing stdin is dropped about half the
// time under load. The client that does exactly that is a one-shot pipe
// (`printf '...process_start...' | ssh box agent-box`), and
// TestSpawn_SurvivesParentExit reproduces it. The library's newest release
// (v1.2.1) keeps the same order, so the guarantee lives here: an agent-box
// that read a request answers it before it exits.
//
// It tracks request ids seen on the read side and removes them when a
// response with the same id is written. Notifications (no id) and the
// server's own notifications are ignored.
type DrainingStdio struct {
	r     io.Reader
	w     io.Writer
	grace time.Duration

	mu      sync.Mutex
	cond    *sync.Cond
	pending map[string]struct{}
	rbuf    []byte // partial line carried between Reads
	wbuf    []byte // partial line carried between Writes
	eof     bool   // underlying reader returned EOF; drain started
}

// NewDrainingStdio wraps r and w. grace <= 0 means DefaultStdioDrainGrace.
func NewDrainingStdio(r io.Reader, w io.Writer, grace time.Duration) *DrainingStdio {
	if grace <= 0 {
		grace = DefaultStdioDrainGrace
	}
	d := &DrainingStdio{r: r, w: w, grace: grace, pending: map[string]struct{}{}}
	d.cond = sync.NewCond(&d.mu)
	return d
}

// Pending reports how many requests have been read but not yet answered.
func (d *DrainingStdio) Pending() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

// Read passes bytes through from the wrapped reader, recording request ids.
// On the wrapped reader's EOF it blocks until no request is pending or the
// grace period elapses, then reports EOF.
func (d *DrainingStdio) Read(p []byte) (int, error) {
