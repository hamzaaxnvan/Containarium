// Package autosleep ticks once a minute and stops user containers whose
// network activity has gone quiet for longer than their per-container
// idle threshold. Wake-on-request is a separate concern (Phase 3); this
// package only decides when to stop.
package autosleep

import (
	"fmt"
	"time"
)

// DecideAction is the verb the ticker should execute for a single
// container after evaluating its inputs. Kept as a typed string so the
// audit log can record it verbatim and tests can compare against
// constants instead of magic literals.
type DecideAction string

const (
	ActionNothing DecideAction = "nothing"
	ActionSleep   DecideAction = "sleep"
)

// DecideInput is the snapshot Decide consumes for one container. All
// observation happens in the caller (the Manager's tick loop); this
// struct is deliberately a plain value type so Decide stays pure and
// testable without any IO or clock dependency.
type DecideInput struct {
	// Username is the container's user-facing handle (Incus container
	// name with the "-container" suffix trimmed). Carried through to
	// the audit log on a sleep so operators can correlate.
	Username string

	// State is the Incus instance status string, e.g. "Running" or
	// "Stopped". Anything other than Running short-circuits to nothing.
	State string

	// AutoSleepEnabled mirrors the Phase 1 opt-in flag.
	AutoSleepEnabled bool

	// IdleThresholdMinutes is the per-container threshold; the daemon
	// applies a default at toggle time, so we trust the value here.
	IdleThresholdMinutes int32

	// IsCoreRole guards platform infra (postgres, caddy, victoriametrics,
	// …) — they never sleep regardless of the flag.
	IsCoreRole bool

	// LastStartedAt is when StartContainer last stamped the Incus user
	// config key. Zero value = unknown (never started during this
	// daemon's awareness of the container).
	LastStartedAt time.Time

	// LastNetworkActivity is the max(last_seen) over the container's
	// traffic_connections rows. Zero value = no traffic ever recorded
	// (clean container, or traffic collector disabled).
	LastNetworkActivity time.Time

	// Now is injected by the caller so unit tests can pin a clock.
	Now time.Time
}

// Decision is the result of evaluating one container's inputs.
type Decision struct {
	Action      DecideAction
	Reason      string // human-readable; the per-sleep audit log records it.
	IdleMinutes int    // populated when Action == ActionSleep, else 0.
}
