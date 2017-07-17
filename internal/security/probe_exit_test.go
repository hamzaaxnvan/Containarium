package security

import (
	"errors"
	"os/exec"
	"testing"
)

// shExit runs script with the host's /bin/sh and returns its exit status.
func shExit(t *testing.T, script string) int {
	t.Helper()
	err := exec.Command("sh", "-c", script).Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("sh could not run: %v", err)
	return -1
}

// Incus reports an exec exit status of 127 as an error, so a presence probe
// must exit 0 or 1 and never 127, whatever /bin/sh is (dash returns 127 for
// `command -v <missing>`; bash returns 1).
func TestPresenceProbeExitsOneWhenMissing(t *testing.T) {
