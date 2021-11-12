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
	}
	if !found {
		t.Fatal("readRunRecord: found = false, want true")
	}
	if got.Name != record.Name || got.PID != record.PID || got.Command != record.Command {
		t.Errorf("readRunRecord = %+v, want %+v", got, record)
	}
	if !got.StartedAt.Equal(record.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, record.StartedAt)
	}
	if got.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil (unreaped run)", *got.ExitCode)
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt = %v, want nil (unreaped run)", *got.FinishedAt)
	}
}

func TestReadRunRecord_MissingReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	restore := setProcessLogDirForTest(dir)
	defer restore()

	_, found, err := readRunRecord("does-not-exist")
	if err != nil {
		t.Fatalf("readRunRecord: unexpected error %v", err)
	}
	if found {
		t.Error("found = true for a record that was never written")
	}
}

// TestWriteRunRecord_ExitCodeZeroDistinctFromAbsent is the regression named
// directly in #1672's design doc: ExitCode/FinishedAt are pointers so a
// clean `exit 0` is never confused with "not yet reaped."
func TestWriteRunRecord_ExitCodeZeroDistinctFromAbsent(t *testing.T) {
	dir := t.TempDir()
	restore := setProcessLogDirForTest(dir)
	defer restore()

	zero := 0
	finishedAt := time.Now()
	record := RunRecord{
		Version:    RunRecordVersion,
		Name:       "clean-exit",
		PID:        999,
		BootID:     "boot-a",
		StartedAt:  time.Now(),
		ExitCode:   &zero,
		FinishedAt: &finishedAt,
	}
	if err := writeRunRecord(record); err != nil {
		t.Fatalf("writeRunRecord: %v", err)
	}
	got, found, err := readRunRecord("clean-exit")
	if err != nil || !found {
		t.Fatalf("readRunRecord: found=%v err=%v", found, err)
	}
	if got.ExitCode == nil {
		t.Fatal("ExitCode = nil, want a pointer to 0")
	}
	if *got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", *got.ExitCode)
	}
	if got.FinishedAt == nil {
		t.Fatal("FinishedAt = nil, want set")
	}
}

