// Package capacity turns a backend's implicit spare resources into an
// explicit, policy-bounded scheduling-headroom signal that the control plane
// can read through the backend surface (ListBackends).
//
// Today a backend's capacity is implicit — the control plane infers it from
// SystemInfo. A box that the auto-sleep loop would scale down can instead
// publish its freed headroom here so the control plane can direct work at it.
// The advertisement is bounded by a local Policy (a time window, excluded
// workload classes, and a safety reservation), so a host running protected
// work never offers it as free. Withdrawing is idempotent. See #680.
package capacity

import (
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// WorkloadClassLabel is the container label that tags a workload's class. A
// container whose class appears in Policy.ExcludedWorkloadClasses is excluded
// from the idle/spare computation, so protected work is never advertised as
// freed headroom.
const WorkloadClassLabel = "user.containarium.workload_class"

// Policy bounds what a backend is willing to advertise. The zero value is a
// valid "always open, no exclusions, offer everything available" policy.
type Policy struct {
	// WindowStartHour / WindowEndHour describe the local-clock hour window
	// during which the advertisement offers non-zero spare. Equal values mean
	// the window is always open. A start greater than the end expresses an
	// overnight window (e.g. 22..6).
	WindowStartHour int32
	WindowEndHour   int32

	// ExcludedWorkloadClasses are workload classes that must not be displaced
	// or co-scheduled. Running containers carrying any of these classes are
	// excluded from the idle/spare computation.
	ExcludedWorkloadClasses []string

	// ReserveFraction is the share of host resources to hold back as a safety
	// reservation, in [0,1). Spare = available * (1 - ReserveFraction).
	ReserveFraction float64
}

// WindowOpen reports whether the policy's advertisement window includes the
// given local time. An always-open window (start == end) is always true.
func (p Policy) WindowOpen(now time.Time) bool {
	if p.WindowStartHour == p.WindowEndHour {
		return true
	}
	// Compare in int space — now.Hour() is already int (0–23), and widening
	// the int32 bounds to int avoids a narrowing int→int32 conversion (G115).
	h := now.Hour()
	start, end := int(p.WindowStartHour), int(p.WindowEndHour)
	if start < end {
		return h >= start && h < end
	}
	// Overnight window wraps midnight, e.g. 22..6.
	return h >= start || h < end
}

// excludes reports whether the policy excludes the given workload class.
func (p Policy) excludes(class string) bool {
