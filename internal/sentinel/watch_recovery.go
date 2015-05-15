package sentinel

import (
	"context"
	"log"
	"time"
)

// Watch-only backend recovery.
//
// The "gcp" backend (initGCPBackend, diagnoseAndRecover in manager.go) is
// this sentinel's primary+failover HTTP proxy target: exactly one per
// process, selected by SelectPrimary. Some fleets also run a second,
// wholly independent GCP spot/preemptible VM that isn't part of that
// traffic pool at all — different tenants, different zone, no shared
// front door — but still wants the same "detect preemption, call
// StartInstance, back off exponentially on repeated failure" treatment
// (#514) instead of silently sitting stopped until a human notices.
//
// AddWatchOnlyBackend registers such a target. It is deliberately kept
// out of the Backend/BackendPool machinery used for HTTP forwarding: a
// watch-only target never becomes m.primary and is never considered by
// SelectPrimary, so it can't accidentally start receiving traffic meant
// for the real primary just because both happen to be "healthy" at the
// same instant.

// watchedBackend tracks recovery state for one watch-only target,
// independent of the Manager-level outageStart/preemptCount/
// recoveryBackoff fields used by the primary "gcp" backend — those
// assume a single outage timeline, which multiple independent targets
// would corrupt if they shared it. Each watchedBackend is touched only
// by its own runWatchOnlyRecovery goroutine, so it needs no lock (same
// single-goroutine-owns-it convention as the primary recovery fields).
type watchedBackend struct {
	id       string
	provider CloudProvider

	down                bool // true once observed stopped/terminated; cleared on running
	recoveryBackoff     time.Duration
	nextRecoveryAttempt time.Time
	lastPreemption      time.Time
	preemptCount        int
	recoveredCount      int
}

// AddWatchOnlyBackend registers an additional GCP-backed instance for
// preemption-watching and auto-restart, without adding it to the primary
// HTTP proxy pool. Call before Run(); id is used only for log lines and
// alert-webhook labeling.
func (m *Manager) AddWatchOnlyBackend(id string, provider CloudProvider) {
	m.watchOnly = append(m.watchOnly, &watchedBackend{id: id, provider: provider})
}

