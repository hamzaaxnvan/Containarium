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
