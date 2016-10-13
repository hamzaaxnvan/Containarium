package audit

import (
	"sync/atomic"
	"time"
)

// persistFailures counts every audit entry this process failed to
// durably write — either the store's Log() call returned an error, or
// the writer's buffered channel was full and the entry was dropped
// before Log() was even attempted. Both are audit-trail gaps: an
// action happened and no row proves it.
//
// The write is always async (HTTPAuditMiddleware, GRPCInterceptor, and
// EventSubscriber all hand entries to a background writer, or in
// EventSubscriber's case run on their own goroutine off the event bus)
// specifically so a slow or unavailable audit store never adds latency
// to — or fails — the request that triggered the entry. That's the
// right trade-off for availability, but it means a write failure was
// previously observable only as a log.Printf line: invisible unless an
// operator happened to be tailing the daemon's stdout at the exact
// moment. This counter makes the failure mode observable without
// giving up the async trade-off.
var (
	persistFailures atomic.Int64
	firstFailureAt  atomic.Int64 // unix nanos; 0 = none recorded yet
)

// recordPersistFailure increments the failure counter and, on the
// first call, latches the time so PersistFailureReport can report
// "since when." Called from all three audit writers (HTTP middleware,
// gRPC interceptor, event subscriber) on both failure modes: a Log()
// error, and a channel-full drop.
func recordPersistFailure() {
	persistFailures.Add(1)
	firstFailureAt.CompareAndSwap(0, time.Now().UnixNano())
