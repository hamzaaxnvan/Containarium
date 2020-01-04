package incus

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lxc/incus/v7/shared/api"
)

// noStateBackoff zeroes stateBackoffBase for the duration of a test so the
// retry loop doesn't actually sleep.
func noStateBackoff(t *testing.T) {
	t.Helper()
	old := stateBackoffBase
	stateBackoffBase = 0
	t.Cleanup(func() { stateBackoffBase = old })
}

// errTransientState mimics the real error text GetInstanceState surfaces
// for a wedged-connection ghost instance (OSS #931) — verbatim, so the
// substring match in isTransientStateErr is exercised.
var errTransientState = errors.New(`Failed to fetch instance "cld-example-container" in project "default": Instance not found`) //nolint:staticcheck // ST1005: mirrors verbatim incus error text

func TestIsTransientStateErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient ghost instance", errTransientState, true},
		{"wrapped transient", fmt.Errorf("failed to get container state: %w", errTransientState), true},
		{"other failure", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := isTransientStateErr(c.err); got != c.want {
			t.Errorf("%s: isTransientStateErr = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestStateWithRetry_RetriesTransientThenSucceeds: the transient
// "Instance not found" error is retried and a later success is returned
// cleanly — reproducing the exact live behavior observed on OSS #931 (the
// same call succeeding on a subsequent attempt with nothing else changed).
func TestStateWithRetry_RetriesTransientThenSucceeds(t *testing.T) {
	noStateBackoff(t)
	calls := 0
	want := &api.InstanceState{Status: "Running"}
	state, _, err := stateWithRetry("test", func() (*api.InstanceState, string, error) {
		calls++
