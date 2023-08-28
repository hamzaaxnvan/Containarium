//go:build !containarium_client

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/logframe"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// shBox is the fake container manager for the box-run reader (#2123): it
// runs each script with the local sh, the way `incus exec <box> sh -c` would
// run it in the box, against a temp dir standing in for /tmp/agent-box.
type shBox struct {
	mu    sync.Mutex
	boxes []string
	// onExec runs before the n-th exec (0-based), so a test can append to a
	// log between follow polls.
	onExec func(n int)
}

func (b *shBox) exec(box, script string) (string, error) {
	b.mu.Lock()
	n := len(b.boxes)
	b.boxes = append(b.boxes, box)
	hook := b.onExec
	b.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	out, err := osexec.Command("sh", "-c", script).Output()
	if err != nil {
		if ee, ok := err.(*osexec.ExitError); ok {
			return "", fmt.Errorf("%v: %s", err, ee.Stderr)
		}
		return "", err
	}
	return string(out), nil
}

func (b *shBox) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.boxes)
}

// boxRunServer is a ContainerServer wired only for the box-run reader, with
// bob as the one collaborator on alice's box.
func boxRunServer(t *testing.T, b *shBox) (*ContainerServer, string) {
	t.Helper()
	dir := t.TempDir()
	s := &ContainerServer{
		boxRunExec:   b.exec,
		boxRunLogDir: dir,
		boxRunPoll:   time.Millisecond,
		boxRunCollaborator: func(owner, subject string) (bool, error) {
			return owner == "alice" && subject == "bob", nil
		},
	}
	return s, dir
}

func hostBootID(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Skip("no /proc boot id on this platform")
	}
	return strings.TrimSpace(string(data))
}

type recordSpec struct {
	name     string
	pid      int
	boot     string
	started  time.Time
	exitCode *int
	framed   bool
}

func writeRecord(t *testing.T, dir string, r recordSpec) {
	t.Helper()
	rec := map[string]any{
		"version": 2, "name": r.name, "pid": r.pid, "boot_id": r.boot,
		"command": "claude -p hi", "cwd": "/home/alice",
		"capture_mode": "combined", "log_path": filepath.Join(dir, r.name+".log"),
		"started_at": r.started,
	}
	if r.framed {
		rec["capture_mode"] = "framed"
	}
	if r.exitCode != nil {
		rec["exit_code"] = *r.exitCode
		rec["finished_at"] = r.started.Add(time.Minute)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, r.name+".json"), string(data))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func frames(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		stream, payload, _ := strings.Cut(p, ":")
		b.Write(logframe.EncodeFrame(logframe.Stream(stream[0]), []byte(payload)))
	}
	return b.String()
}

func exitWith(code int) *int { return &code }

func TestListBoxRuns(t *testing.T) {
	boot := hostBootID(t)
	t0 := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	b := &shBox{}
	s, dir := boxRunServer(t, b)

	writeRecord(t, dir, recordSpec{name: "done", pid: 1 << 22, boot: boot, started: t0, exitCode: exitWith(3)})
	writeRecord(t, dir, recordSpec{name: "live", pid: os.Getpid(), boot: boot, started: t0.Add(2 * time.Hour), framed: true})
	writeRecord(t, dir, recordSpec{name: "rebooted", pid: os.Getpid(), boot: "another-boot", started: t0.Add(time.Hour)})
	writeRecord(t, dir, recordSpec{name: "sidecar", pid: 1 << 22, boot: boot, started: t0.Add(3 * time.Hour)})
	writeFile(t, filepath.Join(dir, "sidecar.exit"), "0 1790000000")
	// Neither a rotated-aside record, a malformed one, nor a future version
	// is a current run.
	writeRecord(t, dir, recordSpec{name: "done.1789990000", pid: 1, boot: boot, started: t0.Add(-time.Hour), exitCode: exitWith(0)})
	writeFile(t, filepath.Join(dir, "broken.json"), "{")
	writeFile(t, filepath.Join(dir, "future.json"), `{"version":99,"name":"future"}`)

	resp, err := s.ListBoxRuns(tenantCtx("alice"), &pb.ListBoxRunsRequest{Username: "alice"})
