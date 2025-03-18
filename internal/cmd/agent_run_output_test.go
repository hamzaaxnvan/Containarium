package cmd

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestPrintAgentRunResult pins what `containarium agent run` prints (#2919):
// a run id is always surfaced with a pointer to crew logs, a server-provided
// message is surfaced, and the stale "Phase 0 seam" line is gone.
func TestPrintAgentRunResult(t *testing.T) {
	tests := []struct {
		name    string
