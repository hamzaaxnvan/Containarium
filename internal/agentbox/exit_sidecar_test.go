//go:build !windows

package agentbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The regression: a run whose connection dropped before it finished
// reported "unknown" forever, because the only thing that wrote the exit
// status was a goroutine inside the agent-box that died with the
// connection. This asserts the CHILD records it instead.
func TestExitSidecar_ChildRecordsItsOwnExit_NoReaperInvolved(t *testing.T) {
	dir := t.TempDir()
	sidecar := exitSidecarPath(dir, "run-a")

	// Run the wrapped command to completion with NO Go-side reaper at all —
	// this is what a detached run looks like after agent-box has exited.
	wrapped := buildRunScript("echo hi; exit 7", sidecar, nil)
	cmd := exec.Command("/bin/sh", "-c", wrapped)
	cmd.Stdout, cmd.Stderr = nil, nil
	err := cmd.Run()

	// The wrapper must preserve the original exit code for any caller that
	// IS still waiting.
	var ee *exec.ExitError
	if err == nil {
		t.Fatal("want non-zero exit from the wrapper")
	} else if !asExitError(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("wrapper changed the exit code: %v", err)
	}

	code, finishedAt, found := readExitSidecar(dir, "run-a")
	if !found {
		t.Fatal("no sidecar written — the detached-run case is still broken")
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if finishedAt.IsZero() {
		t.Error("finishedAt not recorded")
	}
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

func TestApplyExitSidecar(t *testing.T) {
	seven := 7
	tests := []struct {
		name        string
		record      RunRecord
		sidecar     string // "" = none written
		wantCode    *int
