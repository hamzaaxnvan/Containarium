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
