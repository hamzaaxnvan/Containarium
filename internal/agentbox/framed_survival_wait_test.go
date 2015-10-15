//go:build !windows

package agentbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The failure message must tell "the run never started" apart from "it
// started but the sidecar did not appear in time" (#2479).
func TestWaitForExitSidecar_FailureModesAreDistinct(t *testing.T) {
	t.Run("no record is reported as never started", func(t *testing.T) {
		_, err := waitForExitSidecar(t.TempDir(), "r", 100*time.Millisecond)
