package ttlsweeper

import (
	"context"
	"log"
	"sync"
	"time"
)

// DefaultInterval is the tick cadence. Once a minute keeps the
// deletion latency tight (max ~1m past TTL plus graceMargin) without
// flooding incusd with list calls — the typical Containarium daemon
// owns dozens of containers, not thousands.
const DefaultInterval = 60 * time.Second

const (
	// failBackoffBase is the wait after a container's first failed delete; it
	// doubles each consecutive failure up to failBackoffMax. A delete can fail
	// permanently (e.g. a leaked scan mount pinning the ZFS dataset →
	// "dataset is busy", #832) and Decide keeps returning the still-expired
	// box every tick — without backoff the sweeper retries every 60s forever,
	// flooding the journal and never progressing (#831). Backoff turns that
	// into 1m, 2m, 4m, … so one wedged box is near-silent and can't starve the
	// loop.
	failBackoffBase = 1 * time.Minute
	failBackoffMax  = 1 * time.Hour
	// quarantineAfter is the consecutive-failure count past which the sweeper
	// parks the box at failBackoffMax and logs ONCE at WARN — a permanently
	// stuck delete becomes a single actionable line instead of an infinite
	// loop. It still retries hourly in case the blocker clears on its own.
	quarantineAfter = 10
	// maxBackoffShift caps the exponent so failBackoffBase << shift can't
	// overflow on a long-lived stuck entry (the result is clamped to
	// failBackoffMax regardless).
	maxBackoffShift = 16
)

// failState tracks one container's consecutive delete failures so the sweeper
// can back off instead of hammering an undeletable box every tick (#831).
type failState struct {
	count       int
	nextAttempt time.Time
	quarantined bool
}

// IncusClient is the narrow slice of the daemon's container source
// the sweeper actually needs. The wiring PR adapts the real
// *incus.Client (or whatever persists TTL — Incus user.* config,
// SQLite, etc.) into ContainerView records on the way through.
//
// Kept as a one-method interface so the daemon-side implementation
// has zero coupling beyond "produce a slice of view records on
// demand"; tests use a literal fake without mockgen.
type IncusClient interface {
	ListContainers() ([]ContainerView, error)
}

// Deleter is the action seam. The wiring PR provides an
// implementation that invokes the existing DeleteContainer plumbing
// with the right audit-event banner. The sweeper itself does not
// know how a container gets deleted — only that it should be.
//
// reason is a short human-readable string (e.g. "ttl_expired
// 2026-05-23T13:00:00Z") that the implementation may log and/or
// surface in the audit trail. Errors are logged and the loop
// continues — one bad container should not poison the rest of the
