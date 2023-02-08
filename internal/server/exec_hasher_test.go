package server

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
)

// hasherProc is a fake /proc for the exec hasher: <root>/<pid>/exe holding
// content and <root>/<pid>/stat with start time start.
func hasherProc(t *testing.T, root string, pid int, content string, start threatdetect.ProcStartTicks) {
	t.Helper()
	writeStat(t, root, pid, "proc", start)
	if err := os.WriteFile(filepath.Join(root, strconv.Itoa(pid), "exe"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newTestHasher(root string) (*execHasher, *[]threatdetect.ExecEvent) {
	var got []threatdetect.ExecEvent
	h := newExecHasher(func(ev threatdetect.ExecEvent) { got = append(got, ev) })
	h.procRoot = root
	return h, &got
}

// TestExecHasher_PinsPIDBeforeHashing (#2768 item 2): /proc/<pid>/exe is
// hashed only for the process the exec event named. After exe is opened,
// the pid's start time (/proc/<pid>/stat field 22) is checked against the
// event's decode stamp: a process that started after the stamp (beyond one
// tick of slack, startedAfterStamp) took the pid later, so the event is
// dropped instead of getting another binary's hash. An event with no stamp
// cannot be pinned and is dropped.
func TestExecHasher_PinsPIDBeforeHashing(t *testing.T) {
	const pid = 4242
	tests := []struct {
		name      string
		start     threatdetect.ProcStartTicks // the exec event's
		noStat    bool
		wantSum   bool
		wantMoved uint64
	}{
		{name: "stamped in the tick it started", start: fakeStart(pid), wantSum: true},
		{name: "stamped later than it started", start: fakeStart(pid) + 9, wantSum: true},
		{name: "within the one-tick slack", start: fakeStart(pid) - 1, wantSum: true},
		{name: "pid now held by a process started after the stamp", start: fakeStart(pid) - 2, wantMoved: 1},
		{name: "pid now held by a much newer process", start: fakeStart(pid) - 900, wantMoved: 1},
		{name: "no stamp cannot be pinned", start: 0, wantMoved: 1},
		{name: "stat gone", start: fakeStart(pid), noStat: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			hasherProc(t, root, pid, "miner", fakeStart(pid))
			if tt.noStat {
				if err := os.Remove(filepath.Join(root, strconv.Itoa(pid), "stat")); err != nil {
					t.Fatal(err)
				}
			}
			h, got := newTestHasher(root)
			h.process(threatdetect.ExecEvent{PID: pid, StartTime: tt.start, Container: "box-a"})
			if tt.wantSum {
				if len(*got) != 1 || (*got)[0].SHA256 != sha256Hex("miner") || (*got)[0].Container != "box-a" {
					t.Fatalf("redelivered %+v, want one event with the binary's hash", *got)
				}
			} else if len(*got) != 0 {
				t.Fatalf("redelivered %+v for a process that is not the exec's", *got)
			}
			if m := h.pidMoved.Load(); m != tt.wantMoved {
				t.Fatalf("pidMoved = %d, want %d", m, tt.wantMoved)
			}
		})
	}
}

// TestExecHasher_ComparesStartTimeAfterOpen: the order is what makes the pin
// hold. If the pid is reused just before exe is opened, the opened file is
// the newcomer's binary; only a start-time read AFTER the open sees the
// newcomer and drops the event.
func TestExecHasher_ComparesStartTimeAfterOpen(t *testing.T) {
	const pid = 4242
	root := t.TempDir()
	hasherProc(t, root, pid, "miner", fakeStart(pid))
	h, got := newTestHasher(root)
