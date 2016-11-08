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
			want:    RunOutcomeExited,
		},
		{
			name:    "boot differs, exit code absent -> unknown regardless of alive",
			record:  RunRecord{BootID: "boot-old"},
			curBoot: "boot-new",
			alive:   true, // must be ignored: a stale-boot PID can be reassigned
			want:    RunOutcomeUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveOutcome(tc.record, tc.curBoot, func(int) bool { return tc.alive })
			if got != tc.want {
				t.Errorf("ResolveOutcome(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveOutcome_NeverCallsAliveWhenExitCodeSet(t *testing.T) {
	// A killed-box scenario: PID may have been reassigned by the kernel, so
	// isAlive must never even be consulted once ExitCode is authoritative.
	exitCode := 1
	record := RunRecord{BootID: "boot-a", ExitCode: &exitCode}
	called := false
	ResolveOutcome(record, "boot-a", func(int) bool { called = true; return true })
	if called {
		t.Error("alive() must not be called when ExitCode is already set")
	}
}

func TestResolveOutcome_NeverCallsAliveOnBootMismatch(t *testing.T) {
	// A2/boot-mismatch case: consulting liveness on a stale boot is exactly
	// the bug (PID reuse) this design exists to prevent.
	record := RunRecord{BootID: "boot-old"}
	called := false
	ResolveOutcome(record, "boot-new", func(int) bool { called = true; return true })
	if called {
		t.Error("alive() must not be called when the record's boot id differs from the current boot")
	}
}

// ----- writeRunRecord / readRunRecord: atomic round-trip -----

func TestWriteReadRunRecord_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	restore := setProcessLogDirForTest(dir)
	defer restore()

	started := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	record := RunRecord{
		Version:     RunRecordVersion,
		Name:        "my-run",
		PID:         12345,
		BootID:      "boot-a",
		Command:     "sleep 5",
		Cwd:         "/tmp",
		CaptureMode: CaptureCombined,
		LogPath:     filepath.Join(dir, "my-run.log"),
		StartedAt:   started,
	}
	if err := writeRunRecord(record); err != nil {
		t.Fatalf("writeRunRecord: %v", err)
	}

	got, found, err := readRunRecord("my-run")
	if err != nil {
		t.Fatalf("readRunRecord: %v", err)
