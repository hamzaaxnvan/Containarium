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

