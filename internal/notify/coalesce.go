package notify

import (
	"sync"
	"time"
)

// DefaultWindow is the least time between two pushes for one run (#2185).
const DefaultWindow = 30 * time.Second

// Push is one push to send: the open requests of one run that it reports.
type Push struct {
	User     string
	Run      string
	Requests []Request
}

// Coalescer decides when a run's requests are pushed. The first request of a
// run is pushed on the poll that finds it; requests found within window of a
// run's last push wait and go out together, counted, once the window has
// passed. No request is dropped: each one is either pushed or still open in a
// later push, unless it is closed (answered or expired) before its push is
// due, in which case nothing is sent for it.
//
// State is in memory: after a daemon restart, requests still open are pushed
// again (at-least-once).
type Coalescer struct {
	window time.Duration

	mu    sync.Mutex
	state map[runKey]*runState
}

type runKey struct{ user, run string }

type runState struct {
	seen     map[string]bool // request ids pushed or waiting to be
	pending  []string        // ids found but not pushed yet, in order
	lastPush time.Time
}

// NewCoalescer returns a coalescer with the given window.
func NewCoalescer(window time.Duration) *Coalescer {
	return &Coalescer{window: window, state: map[runKey]*runState{}}
}

// Step takes the open requests of every live run of user, keyed by run name,
// and returns the pushes now due. A run of user missing from open has ended,
// and its state is dropped.
func (c *Coalescer) Step(user string, open map[string][]Request, now time.Time) []Push {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.state {
		if _, ok := open[k.run]; k.user == user && !ok {
			delete(c.state, k)
		}
	}
	var out []Push
	for run, reqs := range open {
		k := runKey{user, run}
		st := c.state[k]
		if st == nil {
			st = &runState{seen: map[string]bool{}}
			c.state[k] = st
		}
		byID := make(map[string]Request, len(reqs))
