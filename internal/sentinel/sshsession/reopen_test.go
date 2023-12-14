//go:build !windows

package sshsession

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// #2415 — logrotate (create mode) renames the sink; the plugin must follow
// the PATH again on SIGHUP, or it keeps appending to the rotated file.

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestJSONLRecorder_ReopenFollowsThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh-sessions.jsonl")
	rec, err := NewJSONLFileRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Close() }()

	at := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if err := rec.Record(Record{SessionID: "before", Phase: SessionPhaseOpen, OccurredAt: at}); err != nil {
		t.Fatal(err)
	}

	// logrotate: rename, then (create mode) a fresh empty file at the path.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := rec.Reopen(); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if err := rec.Record(Record{SessionID: "after", Phase: SessionPhaseOpen, OccurredAt: at}); err != nil {
		t.Fatal(err)
	}

	if old := readFile(t, path+".1"); !strings.Contains(old, `"before"`) || strings.Contains(old, `"after"`) {
		t.Fatalf("rotated file must hold only pre-rotation records, got %q", old)
	}
	if cur := readFile(t, path); !strings.Contains(cur, `"after"`) || strings.Contains(cur, `"before"`) {
		t.Fatalf("new file must hold only post-rotation records, got %q", cur)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("reopened sink mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestJSONLRecorder_ReopenWithoutFileIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	rec := NewJSONLRecorder(&buf)
	if err := rec.Reopen(); err != nil {
		t.Fatalf("a writer-backed recorder has nothing to reopen: %v", err)
	}
	if err := rec.Record(Record{SessionID: "x", Phase: SessionPhaseOpen}); err != nil || buf.Len() == 0 {
		t.Fatalf("recorder must keep writing after a no-op Reopen: %v", err)
	}
}

// A failed reopen must leave the old handle in place: losing records is
// worse than writing them to the rotated file.
func TestJSONLRecorder_FailedReopenKeepsWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "ssh-sessions.jsonl")
	rec, err := NewJSONLFileRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Close() }()
	// Make the path unopenable: replace its directory with a plain file.
	if err := os.Rename(filepath.Join(dir, "sub"), filepath.Join(dir, "sub.moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rec.Reopen(); err == nil {
		t.Fatal("reopen into an unopenable path must error")
	}
	if err := rec.Record(Record{SessionID: "still-written", Phase: SessionPhaseOpen}); err != nil {
		t.Fatalf("recorder must keep its old handle after a failed reopen: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "sub.moved", "ssh-sessions.jsonl")); !strings.Contains(got, "still-written") {
		t.Fatalf("record lost: %q", got)
	}
}

func TestJSONLRecorder_ReopenConcurrentWithRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	rec, err := NewJSONLFileRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rec.Close() }()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = rec.Record(Record{SessionID: "c", Phase: SessionPhaseOpen})
