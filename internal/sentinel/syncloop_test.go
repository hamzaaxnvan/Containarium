package sentinel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// #953: both sync loops drove attempts off a plain ticker, so one transient
// failure cost a full interval of staleness — up to 6h for certsync.
//
// The schedule tests below are the load-bearing ones: they pin the two
// properties that make a single shared schedule safe for a 2m loop and
// useful for a 6h one.

func TestSyncRetryDelays_KeysyncBurstFitsInsideItsInterval(t *testing.T) {
	const interval = 2 * time.Minute
	delays := syncRetryDelays(interval)

	if len(delays) == 0 {
		t.Fatal("no fast retries for the 2m keysync loop: a transient failure would " +
			"still cost a full interval, which is the bug (#953)")
	}

	var total time.Duration
	for _, d := range delays {
		total += d
	}
	// The burst must finish before the next regular tick. Otherwise
	// attempts stack on top of each other and the loop runs two
	// overlapping sync schedules.
	if total >= interval {
		t.Errorf("retry burst totals %s for a %s interval — it would outlive its own "+
			"interval and stack attempts; delays=%v", total, interval, delays)
	}
}

func TestSyncRetryDelays_CertsyncRecoversInMinutesNotHours(t *testing.T) {
	const interval = 6 * time.Hour
	delays := syncRetryDelays(interval)

	if len(delays) != maxSyncFastRetries {
		t.Errorf("got %d retries for a %s interval, want the full %d — a long interval "+
			"is precisely where the burst matters", len(delays), interval, maxSyncFastRetries)
	}

	var total time.Duration
	for _, d := range delays {
		total += d
	}
	// The point of the issue: a blip must not cost most of a day.
	if total > 30*time.Minute {
		t.Errorf("retry burst spans %s; that is too close to just waiting for the tick", total)
	}
	if total < time.Minute {
		t.Errorf("retry burst spans only %s — too short to survive a daemon restart, "+
			"which is the failure this exists to absorb", total)
	}
}

// Delays must grow. A flat schedule either hammers a down backend or gives
// up too soon; the doubling is what lets one schedule serve both loops.
func TestSyncRetryDelays_AreNonDecreasingAndCapped(t *testing.T) {
	delays := syncRetryDelays(6 * time.Hour)
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Errorf("delay %d (%s) is shorter than delay %d (%s); the schedule must back off",
				i, delays[i], i-1, delays[i-1])
		}
	}
	for i, d := range delays {
		if d > syncRetryMax {
			t.Errorf("delay %d is %s, above the %s cap", i, d, syncRetryMax)
		}
	}
	if len(delays) > 0 && delays[0] != syncRetryInitial {
		t.Errorf("first retry is %s, want %s", delays[0], syncRetryInitial)
	}
}

