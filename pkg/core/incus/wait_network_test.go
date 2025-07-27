package incus

import (
	"errors"
	"testing"
	"time"
)

// noWaitNetBackoff shrinks the poll bounds for the duration of a test so the
// polling loop doesn't actually sleep for meaningful wall-clock time.
func noWaitNetBackoff(t *testing.T) {
	t.Helper()
	oldMin, oldMax := waitNetPollMin, waitNetPollMax
	waitNetPollMin, waitNetPollMax = 0, 0
	t.Cleanup(func() { waitNetPollMin, waitNetPollMax = oldMin, oldMax })
}

// TestWaitForIP_ReturnsAsSoonAsTheLeaseLands is the regression test for the
// flat 1s sleep. The fake leases on the 2nd attempt; with the old loop that
// took ~1s regardless, so an assertion well under 1s fails against it and
// passes against the backoff.
func TestWaitForIP_ReturnsAsSoonAsTheLeaseLands(t *testing.T) {
	calls := 0
	get := func() (string, error) {
		calls++
		if calls < 2 {
			return "", nil
		}
		return "10.0.0.5", nil
	}

	start := time.Now()
	ip, err := waitForIP(30*time.Second, get)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitForIP err = %v, want nil", err)
	}
	if ip != "10.0.0.5" {
		t.Errorf("ip = %q, want 10.0.0.5", ip)
	}
	// The first backoff is waitNetPollMin (25ms). A generous ceiling that is
	// still far below the 1s quantum this replaces.
	if elapsed > 200*time.Millisecond {
