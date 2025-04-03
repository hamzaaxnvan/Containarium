package incus

import (
	"errors"
	"fmt"
	"testing"
)

// noBackoff zeroes execBackoffBase for the duration of a test so the
// retry loop doesn't actually sleep.
func noBackoff(t *testing.T) {
	t.Helper()
	old := execBackoffBase
	execBackoffBase = 0
	t.Cleanup(func() { execBackoffBase = old })
}

// errTransient mimics the incus exec PID-tracking failure as it surfaces
// from the wrapper (wrapped in "command execution failed: …").
// The inner message mirrors incus's real error text verbatim so the
// substring match in isTransientExecErr is exercised; keep its casing.
var errTransient = fmt.Errorf("command execution failed: %w",
	errors.New("Failed to retrieve PID of executing child process")) //nolint:staticcheck // ST1005: mirrors verbatim incus error text

func TestIsTransientExecErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient PID race", errTransient, true},
		{"wrapped transient", fmt.Errorf("exec foo: %w", errTransient), true},
		{"real non-zero exit", errors.New("command exited with code 1"), false},
		{"other failure", errors.New("instance not found"), false},
	}
	for _, c := range cases {
		if got := isTransientExecErr(c.err); got != c.want {
			t.Errorf("%s: isTransientExecErr = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestExecWithRetry_RetriesTransientThenSucceeds: the transient PID error
// is retried and a later success is returned cleanly.
func TestExecWithRetry_RetriesTransientThenSucceeds(t *testing.T) {
	noBackoff(t)
	calls := 0
	err := execWithRetry("test", func() error {
