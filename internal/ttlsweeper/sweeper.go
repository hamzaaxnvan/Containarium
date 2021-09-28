package ttlsweeper

import "time"

// ContainerView is the slice of container state the sweeper actually
// reads. Decoupled from incus.ContainerInfo (and from the generated
// pb.Container) so this package has zero dependency on either — easier
// to test, and the daemon-wiring follow-up can adapt whichever source
// it picks (Incus user.* config keys, SQLite row, etc.) at the call
// site without touching the decision logic.
type ContainerView struct {
	// Name is the Incus container name (e.g. "alice-container").
	// Echoed back in Decide's returned slice and used by the wiring
	// PR's Deleter as the delete key.
	Name string

	// TTLExpiresAt is the wall-clock time at which the container
	// should be auto-deleted. nil = no TTL set (container persists
	// indefinitely). Pointer-not-zero-value so we can distinguish
	// "unset" from "set to the zero time" — important because Decide
	// must treat unset as "skip" rather than "delete immediately".
	TTLExpiresAt *time.Time

	// Stopped is true when the container is in the STOPPED state. Only a
	// stopped box is eligible for the stopped→delete timer (#525); a running
	// box is reaped only by the absolute TTLExpiresAt above.
	Stopped bool

	// StoppedAt is when the box most recently became STOPPED (cleared on
	// start, so a woken box resets the clock). nil = not known to be stopped.
	// The stopped→delete timer runs from here.
	StoppedAt *time.Time

	// DeleteAfterStopped is the per-box stopped→delete window (#525). nil =
	// the box never opted into stopped→delete (the safe default — a
	// scale-to-zero box that merely sleeps is never reaped for being
	// stopped). Set + Stopped + StoppedAt+window elapsed → delete.
	DeleteAfterStopped *time.Duration

	// Protected marks a box that must never be auto-reaped (#284, delete-policy
	// = protected — e.g. a persistent runner). When true, Decide skips the box
	// regardless of any TTL or stopped→delete window. Defense in depth: a
	// protected box should never carry a TTL, but if one is stamped by mistake
	// the box still survives the sweeper.
	Protected bool
}

// graceMargin is subtracted from the comparison clock before checking
// whether ttl_expires_at has elapsed. Protects against minor clock
// skew between the daemon host and whatever set the TTL (the GHA
// runner, a developer's laptop, etc.). 30s is generous compared to
