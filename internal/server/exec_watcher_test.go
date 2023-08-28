package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cilium/ebpf/ringbuf"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeTree builds a fake /proc + /sys/fs/cgroup under a temp dir: for each
// pid → cgroup path, a /proc/<pid>/cgroup file naming it and the cgroup
// directory itself. Returns the two roots and the inode of every cgroup
// directory on each path (the leaf and all its ancestors), keyed by path.
func fakeTree(t *testing.T, pids map[int64]string) (procRoot, cgroupRoot string, inode map[string]uint64) {
	t.Helper()
	base := t.TempDir()
	procRoot = filepath.Join(base, "proc")
	cgroupRoot = filepath.Join(base, "cgroup")
	inode = map[string]uint64{}
	for pid, cg := range pids {
		dir := filepath.Join(procRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Real files carry a v1 line or two on hybrid hosts; the resolver
		// must pick the unified "0::" line.
		content := "1:name=systemd:/ignored\n0::/" + cg + "\n"
		if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		cgDir := filepath.Join(cgroupRoot, cg)
		if err := os.MkdirAll(cgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(cg, "/")
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			st, err := os.Stat(filepath.Join(cgroupRoot, prefix))
			if err != nil {
				t.Fatal(err)
			}
			inode[prefix] = st.Sys().(*syscall.Stat_t).Ino
		}
	}
	return procRoot, cgroupRoot, inode
}

// TestContainerCgroupRoot: the id registered for a box is its payload root,
// so an exec anywhere under it — init.scope, system.slice/ssh.service, a
// user session — has that id among its ancestors.
func TestContainerCgroupRoot(t *testing.T) {
	tests := []struct{ in, want string }{
		{"lxc.payload.box-a/init.scope", "lxc.payload.box-a"},
		{"lxc.payload.box-a/system.slice/ssh.service", "lxc.payload.box-a"},
		{"lxc.payload.box-a/user.slice/user-1000.slice/session-3.scope", "lxc.payload.box-a"},
		{"lxc.payload.box/.lxc", "lxc.payload.box"},
		{"lxc.payload.box-b", "lxc.payload.box-b"},
		{"/lxc.payload.box-b/", "lxc.payload.box-b"},
		{"user.slice/user-1000.slice/foo.service", "user.slice/user-1000.slice/foo.service"}, // no payload component: unchanged
	}
	for _, tc := range tests {
		if got := containerCgroupRoot(tc.in); got != tc.want {
			t.Errorf("containerCgroupRoot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCgroupIDForPID(t *testing.T) {
	procRoot, cgroupRoot, ino := fakeTree(t, map[int64]string{
		4242: "lxc.payload.box-a/init.scope",           // systemd box: init sits in init.scope under the payload root
		4300: "lxc.payload.box-b",                      // init still at the payload root
		4400: "lxc.payload.box-c/.lxc",                 // liblxc's own cgroup under the root
		4500: "user.slice/user-1000.slice/foo.service", // no payload component: the leaf itself
		// An SSH session's shell in box-a, a SIBLING of init.scope: never read
		// by the resolver (only init pids are), but it must share the
		// registered ancestor — asserted below.
		4243: "lxc.payload.box-a/system.slice/ssh.service",
	})
	// A pid whose /proc entry has no unified line.
	if err := os.MkdirAll(filepath.Join(procRoot, "5000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procRoot, "5000", "cgroup"), []byte("1:cpu:/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A pid whose cgroup path names a directory that no longer exists.
	if err := os.MkdirAll(filepath.Join(procRoot, "5001"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procRoot, "5001", "cgroup"), []byte("0::/gone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A pid whose cgroup path tries to escape the cgroup root.
	if err := os.MkdirAll(filepath.Join(procRoot, "5002"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procRoot, "5002", "cgroup"), []byte("0::/../proc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		pid     int64
		want    uint64
		wantErr string
	}{
		{name: "init in init.scope registers the payload root, not the leaf", pid: 4242, want: ino["lxc.payload.box-a"]},
		{name: "init at the payload root", pid: 4300, want: ino["lxc.payload.box-b"]},
		{name: "init under .lxc registers the payload root", pid: 4400, want: ino["lxc.payload.box-c"]},
		{name: "no payload component falls back to the leaf", pid: 4500, want: ino["user.slice/user-1000.slice/foo.service"]},
		{name: "an ssh session's cgroup resolves to the same registered root", pid: 4243, want: ino["lxc.payload.box-a"]},
		{name: "non-positive pid", pid: 0, wantErr: "not running"},
		{name: "missing /proc entry", pid: 9999, wantErr: "no such file"},
		{name: "no unified line", pid: 5000, wantErr: "no cgroup v2 path"},
		{name: "cgroup directory gone", pid: 5001, wantErr: "no such file"},
		{name: "path escaping the root is refused", pid: 5002, wantErr: "escapes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cgroupIDForPID(procRoot, cgroupRoot, tc.pid)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("cgroupIDForPID: %v", err)
			}
			if got != tc.want || got == 0 {
				t.Fatalf("id = %d, want %d", got, tc.want)
			}
		})
	}
	// The registered id must never be the leaf: an exec in
	// system.slice/ssh.service would otherwise have no watched ancestor.
	if ino["lxc.payload.box-a/init.scope"] == ino["lxc.payload.box-a"] {
		t.Fatal("fixture: leaf and root share an inode; the test cannot distinguish them")
	}
}

// fakeCgroupMap records WatchCgroup/UnwatchCgroup calls like the kernel map.
type fakeCgroupMap struct {
	mu      sync.Mutex
	entries map[uint64]uint32
	watched []uint64
	removed []uint64
	ops     []string // "watch <id>" / "unwatch <id>", in call order
	err     error
}

func newFakeCgroupMap() *fakeCgroupMap { return &fakeCgroupMap{entries: map[uint64]uint32{}} }

func (m *fakeCgroupMap) WatchCgroup(id uint64, idx uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.entries[id] = idx
	m.watched = append(m.watched, id)
	m.ops = append(m.ops, fmt.Sprintf("watch %d", id))
	return nil
}

func (m *fakeCgroupMap) UnwatchCgroup(id uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
	m.removed = append(m.removed, id)
	m.ops = append(m.ops, fmt.Sprintf("unwatch %d", id))
	return nil
}

func (m *fakeCgroupMap) ids() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint64, 0, len(m.entries))
	for id := range m.entries {
		out = append(out, id)
	}
	return out
}

func running(name string, pid int64, opts ...func(*incus.ContainerInfo)) incus.ContainerInfo {
	c := incus.ContainerInfo{Name: name, State: "Running", Pid: pid, Tenant: "t-" + name}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func newTestWatcher(t *testing.T, procRoot, cgroupRoot string, m *fakeCgroupMap) *ExecWatcher {
	t.Helper()
	w := NewExecWatcher(nil)
	w.procRoot, w.cgroupRoot = procRoot, cgroupRoot
	w.maps = m
	w.setStatus(execTelemetryOK)
	return w
}

// TestWatchedCgroupsReconcile: started containers are added (by their cgroup
// inode), running ones kept, stopped ones removed, a restart (new init pid →
// new cgroup inode) swaps the entry, and the control plane, core-role
// containers and anything without a pid are never registered.
func TestWatchedCgroupsReconcile(t *testing.T) {
	procRoot, cgroupRoot, ino := fakeTree(t, map[int64]string{
		100: "lxc.payload.box-a",
		200: "lxc.payload.box-b",
		201: "lxc.payload.box-b.restarted", // box-b after a restart: new init pid, new cgroup dir
		300: "lxc.payload.core-postgres",
		400: "lxc.payload.cloud-cp",
	})
	m := newFakeCgroupMap()
	w := newTestWatcher(t, procRoot, cgroupRoot, m)

	w.Reconcile([]incus.ContainerInfo{
		running("box-a", 100),
		running("box-b", 200),
		running("core-postgres", 300, func(c *incus.ContainerInfo) { c.Role = incus.RolePostgres }),
		running("cloud-cp", 400, func(c *incus.ContainerInfo) { c.Role = incus.RoleControlPlane }),
		running("box-stopped", 0, func(c *incus.ContainerInfo) { c.State = "Stopped" }),
		running("box-nopid", 0),
	})
	want := map[uint64]string{ino["lxc.payload.box-a"]: "box-a", ino["lxc.payload.box-b"]: "box-b"}
	got := m.ids()
	if len(got) != 2 {
		t.Fatalf("watched after first reconcile = %v, want exactly box-a and box-b", got)
	}
	for _, id := range got {
		name, ok := w.lookup(id)
		if !ok || want[id] != name.Container {
			t.Errorf("id %d → %+v, want %q", id, name, want[id])
		}
		if name.TenantID != "t-"+want[id] {
			t.Errorf("id %d tenant = %q, want %q", id, name.TenantID, "t-"+want[id])
		}
	}

	// Steady state: same containers → no new writes, nothing removed.
	before := len(m.watched)
	w.Reconcile([]incus.ContainerInfo{running("box-a", 100), running("box-b", 200)})
	if len(m.watched) != before || len(m.removed) != 0 {
		t.Fatalf("steady reconcile wrote %d / removed %d entries, want 0 / 0", len(m.watched)-before, len(m.removed))
	}

	// box-b restarts (new pid 201 → new cgroup inode): old id removed, new added; box-a stops: removed.
	w.Reconcile([]incus.ContainerInfo{running("box-b", 201), running("box-a", 0, func(c *incus.ContainerInfo) { c.State = "Stopped" })})
	got = m.ids()
	if len(got) != 1 || got[0] != ino["lxc.payload.box-b.restarted"] {
		t.Fatalf("watched after restart/stop = %v, want only box-b's new id %d", got, ino["lxc.payload.box-b.restarted"])
	}
	if _, ok := w.lookup(ino["lxc.payload.box-a"]); ok {
		t.Error("box-a still attributable after it stopped")
	}
	if _, ok := w.lookup(ino["lxc.payload.box-b"]); ok {
		t.Error("box-b's old cgroup id still attributable after its restart")
	}
	if len(m.removed) != 2 {
		t.Errorf("removed = %v, want box-a's id and box-b's old id", m.removed)
	}

	// A container gone from the list entirely is removed too.
	w.Reconcile(nil)
	if ids := m.ids(); len(ids) != 0 {
		t.Fatalf("watched after an empty reconcile = %v, want none", ids)
	}
}

// Reconcile before Start (no kernel map yet) must not panic and must not
// remember anything.
func TestWatchedCgroupsReconcile_BeforeStartIsNoop(t *testing.T) {
	procRoot, cgroupRoot, _ := fakeTree(t, map[int64]string{100: "lxc.payload.box-a"})
	w := NewExecWatcher(nil)
	w.procRoot, w.cgroupRoot = procRoot, cgroupRoot
	w.Reconcile([]incus.ContainerInfo{running("box-a", 100)})
	if n := w.watchedCount(); n != 0 {
		t.Fatalf("watched = %d before Start, want 0", n)
	}
}

func kernelEvent(cg uint64, pid uint32, comm string, argv ...string) netbpf.ExecEvent {
	return netbpf.ExecEvent{HostPID: pid, PPID: 1, UID: 1000, CgroupID: cg, Comm: comm, Filename: "/usr/bin/" + comm, Argv: argv}
}

// TestExecWatcher_AttributesAndHandsToEngine: a decoded event for a watched
// cgroup reaches the engine hook as a threatdetect.ExecEvent with the
// container and tenant attached and argv joined; an event for an unknown
// cgroup is dropped and counted, never attributed to the wrong box.
func TestExecWatcher_AttributesAndHandsToEngine(t *testing.T) {
	procRoot, cgroupRoot, ino := fakeTree(t, map[int64]string{100: "lxc.payload.box-a"})
	m := newFakeCgroupMap()
	var got []threatdetect.ExecEvent
	w := newTestWatcher(t, procRoot, cgroupRoot, m)
	w.engine = func(ev threatdetect.ExecEvent) { got = append(got, ev) }
	w.now = func() time.Time { return time.Unix(1700000000, 0) }
	w.Reconcile([]incus.ContainerInfo{running("box-a", 100)})

	w.OnExecEvent(context.Background(), kernelEvent(ino["lxc.payload.box-a"], 4242, "xmrig", "./xmrig", "-o", "pool:3333"))
	w.OnExecEvent(context.Background(), kernelEvent(777777, 1, "sh", "sh")) // never registered

	if len(got) != 1 {
		t.Fatalf("engine got %d events, want 1: %+v", len(got), got)
	}
	ev := got[0]
	if ev.Container != "box-a" || ev.TenantID != "t-box-a" || ev.PID != 4242 || ev.PPID != 1 || ev.UID != 1000 ||
		ev.Comm != "xmrig" || ev.Filename != "/usr/bin/xmrig" || ev.ArgvString() != "./xmrig -o pool:3333" || !ev.At.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("engine event = %+v", ev)
	}
	if w.Unattributed() != 1 {
		t.Fatalf("unattributed = %d, want 1", w.Unattributed())
	}
}

// TestExecWatcher_KeepsArgumentBoundaries (#2768): the engine gets the real
// NUL-separated argument vector, so an argument holding spaces (a commit
// message) stays one argument for the argv matchers.
func TestExecWatcher_KeepsArgumentBoundaries(t *testing.T) {
	procRoot, cgroupRoot, ino := fakeTree(t, map[int64]string{100: "lxc.payload.box-a"})
	var got []threatdetect.ExecEvent
	w := newTestWatcher(t, procRoot, cgroupRoot, newFakeCgroupMap())
	w.engine = func(ev threatdetect.ExecEvent) { got = append(got, ev) }
	w.Reconcile([]incus.ContainerInfo{running("box-a", 100)})

	want := []string{"git", "commit", "-m", "document stratum+tcp:// pool format"}
	w.OnExecEvent(context.Background(), kernelEvent(ino["lxc.payload.box-a"], 4242, "git", want...))
	if len(got) != 1 || !slices.Equal(got[0].Argv, want) {
		t.Fatalf("engine events = %+v, want one with argv %q", got, want)
	}
}

// TestExecWatcher_StampsExecsWithBootClock (#2765): the watcher stamps
// every decoded exec with the boot clock and hands the stamp to the engine
// as StartTime, including an exec whose process already exited (the kill
// sorts that out against the live pid); a failed clock read gives no stamp
// (0), never a guessed one, and is counted. Nothing under /proc/<pid> is
// opened for it (procOpenTrap catches any open, through any code path).
func TestExecWatcher_StampsExecsWithBootClock(t *testing.T) {
	procRoot, cgroupRoot, ino := fakeTree(t, map[int64]string{100: "lxc.payload.box-a"})
	opens := procOpenTrap(t, procRoot, 4242, "my (miner)", 987654, procPerExecFiles...)
	m := newFakeCgroupMap()
