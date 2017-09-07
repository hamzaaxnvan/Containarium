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
