package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// sentryHealthInterval is how often SentryHealth re-checks the sentry. The
// first check runs immediately at startup, so a fault is recorded well inside
// the 60 s the #2634 acceptance criterion allows.
const sentryHealthInterval = 60 * time.Second

// sentryHealthStore is what SentryHealth needs from a finding store: the
// engine's deduping upsert plus Resolve (to close a finding on recovery) and
// List (to adopt a row an earlier process left open). *FindingStore and
// *MemFindingStore both satisfy it.
type sentryHealthStore interface {
	threatdetect.FindingSink
	threatdetect.FindingReader
}

// SentryHealth raises a MEDIUM SENTRY_UNAVAILABLE finding while an armed
// backend's sentry is not running cleanly — UNAVAILABLE, DEGRADED, or any
// rule unhealthy — and resolves it once the sentry reports OK (#2634; design
// component 6 in docs/architecture/process-exec-guardrail.md).
//
// Deliberately not a threatdetect.Rule: a rule only runs inside a working
// engine, and the faults this reports are exactly the ones where the engine
// is missing or broken. The finding goes through the ordinary store, so the
// store's notifier delivers it to the webhook like any other finding.
type SentryHealth struct {
	status    func() *pb.GetSentryStatusResponse
	store     sentryHealthStore
	backendID func() string
	newTicker func(time.Duration) (<-chan time.Time, func())
	interval  time.Duration

	mu      sync.Mutex
	adopted bool             // rows left open by an earlier process were loaded
	open    map[int64]string // open SENTRY_UNAVAILABLE finding id -> subject
}

// NewSentryHealth builds the loop. status is the unauthenticated sentry
// status (ThreatDetectionServer.sentryStatus); backendID is read at write
// time because the local backend id is only known once the peer pool
// starts. newTicker nil means a real time.Ticker.
func NewSentryHealth(status func() *pb.GetSentryStatusResponse, store sentryHealthStore, backendID func() string, newTicker func(time.Duration) (<-chan time.Time, func()), interval time.Duration) *SentryHealth {
	if newTicker == nil {
		newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	if backendID == nil {
		backendID = func() string { return "" }
	}
	return &SentryHealth{
		status:    status,
		store:     store,
		backendID: backendID,
		newTicker: newTicker,
		interval:  interval,
		open:      make(map[int64]string),
	}
}

// newSentryHealthIfArmed returns nil — no loop, nothing ever raised — when
// CONTAINARIUM_THREAT_SENTRY is unset (unarmed is a choice, not a fault), or
// when there is no finding store to write into (it needs the audit store,
// which needs Postgres; dual_server logs that case).
func newSentryHealthIfArmed(armed bool, status func() *pb.GetSentryStatusResponse, store sentryHealthStore, backendID func() string) *SentryHealth {
	if !armed || store == nil {
		return nil
	}
	return NewSentryHealth(status, store, backendID, nil, sentryHealthInterval)
}

// Run checks once immediately, then every interval, until ctx is done.
func (h *SentryHealth) Run(ctx context.Context) {
	h.Check(ctx)
	ticks, stop := h.newTicker(h.interval)
	defer stop()
	for {
