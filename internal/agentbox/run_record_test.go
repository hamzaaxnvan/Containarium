//go:build !windows

package agentbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// setProcessLogDirForTest points processLogDir at an isolated temp dir (A1
// in the design doc: tests must not touch the real /tmp/agent-box) and
// returns a restore func for the caller to defer.
func setProcessLogDirForTest(dir string) func() {
	prev := processLogDir
	processLogDir = dir
	return func() { processLogDir = prev }
}

func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func fileExistsForTest(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ----- ResolveOutcome: pure, table-driven per the #1672 design doc's 5-row
// matrix (docs/architecture/remote-coding-agent.md, Part A). -----

func TestResolveOutcome(t *testing.T) {
	exitCode := 0
	cases := []struct {
		name    string
		record  RunRecord
		curBoot string
		alive   bool
		want    RunOutcome
	}{
		{
			name:    "boot matches, exit code set -> exited",
			record:  RunRecord{BootID: "boot-a", ExitCode: &exitCode},
			curBoot: "boot-a",
			alive:   true, // ExitCode set is authoritative regardless of alive
			want:    RunOutcomeExited,
		},
		{
			name:    "boot matches, exit code absent, alive -> running",
			record:  RunRecord{BootID: "boot-a"},
			curBoot: "boot-a",
			alive:   true,
			want:    RunOutcomeRunning,
		},
		{
			name:    "boot matches, exit code absent, not alive -> unknown",
			record:  RunRecord{BootID: "boot-a"},
			curBoot: "boot-a",
			alive:   false,
			want:    RunOutcomeUnknown,
		},
		{
			name:    "boot differs, exit code set -> exited",
			record:  RunRecord{BootID: "boot-old", ExitCode: &exitCode},
			curBoot: "boot-new",
			alive:   true,
