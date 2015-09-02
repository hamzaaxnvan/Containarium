//go:build ebpf_load && linux

// Real-kernel coverage for the execwatch eBPF object (#2637), in the same
// lane as ebpf_load_test.go (.github/workflows/ebpf-load.yml, root, a BTF
// kernel). Everything else in this package tests the Go side against fakes;
// this file is where the compiled object is proven to pass the verifier with
// CO-RE against the running kernel, attach to sched/sched_process_exec, and
// emit exactly the events the design promises:
//
//   - an exec inside a cgroup registered in watched_cgroups → one event with
//     that cgroup id, comm, filename and argv;
//   - a host exec (cgroup not registered) → nothing;
//   - a 10k-exec burst with nobody draining the ring → a lost count, not a
//     crash, and the ring still readable afterwards;
//   - a paced 1,000 execs/s load for 60 s from a separate process, with and
//     without the program → the kernel + consumer CPU as a share of the
//     host, asserted under the < 1 % bar, with the batched ring wakeups
//     (#2774) compared against a wakeup per record.
//
// Cgroup placement uses clone3(CLONE_INTO_CGROUP) through exec.Cmd's
// UseCgroupFD, so the exec'ing child is in the test cgroup from its first
// instruction and the one event is unambiguous.
package netbpf

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
	"golang.org/x/sys/unix"
)

const (
	ebpfLoadExecObjPath = "execwatch.bpf.o" // built next to netpolicy.bpf.o by `make build-bpf`
	ebpfLoadCgroupRoot  = "/sys/fs/cgroup"
)

// testCgroup is a throwaway cgroup v2 leaf under the root, with the inode the
// kernel reports as its cgroup id and an fd for CLONE_INTO_CGROUP.
type testCgroup struct {
	path string
	id   uint64
	fd   int
}

func newTestCgroup(t *testing.T, label string) *testCgroup {
	t.Helper()
	path := filepath.Join(ebpfLoadCgroupRoot, fmt.Sprintf("ebpfload-%s-%d", label, os.Getpid()))
	if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
		t.Fatalf("mkdir %s: %v (is cgroup v2 mounted at %s?)", path, err, ebpfLoadCgroupRoot)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %s: no Stat_t", path)
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	cg := &testCgroup{path: path, id: sys.Ino, fd: fd}
	t.Cleanup(func() {
		_ = syscall.Close(cg.fd)
		// Children have been waited for; a leaf with no processes removes
		// cleanly. Retry briefly in case the kernel is still reaping.
		for i := 0; i < 50; i++ {
			if err := os.Remove(cg.path); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("could not remove %s (left behind)", cg.path)
	})
	return cg
}

// run executes argv, inside cg when cg is non-nil, and waits for it.
func runExec(t *testing.T, cg *testCgroup, argv ...string) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- fixed test argv
	if cg != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: cg.fd}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
}

// collectingSink records events; the tests poll it.
type collectingSink struct {
	mu     sync.Mutex
	events []ExecEvent
}

func (s *collectingSink) OnExecEvent(_ context.Context, ev ExecEvent) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}

func (s *collectingSink) snapshot() []ExecEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExecEvent(nil), s.events...)
}

// startConsumer drains w's ring into a sink until the test ends.
func startConsumer(t *testing.T, w *ExecWatch) *collectingSink {
	t.Helper()
	rd, err := w.NewExecReader()
	if err != nil {
		t.Fatalf("NewExecReader: %v", err)
	}
	sink := &collectingSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ConsumeExecEvents(ctx, rd, sink, func(err error) { t.Logf("consumer: %v", err) })
	}()
	t.Cleanup(func() {
		// ConsumeExecEvents returns within one tick of cancel. Close only
		// after it has: its AvailableBytes is unlocked and Close unmaps the
		// ring (#2812 review).
		cancel()
		<-done
		_ = rd.Close()
	})
	return sink
}

func loadAttached(t *testing.T) *ExecWatch {
	t.Helper()
	w, err := LoadExecWatch(ebpfLoadExecObjPath)
	if err != nil {
		t.Fatalf("LoadExecWatch(%q): %v", ebpfLoadExecObjPath, err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Attach(); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return w
}

// TestExecWatch_RealObjectLoadsAndAttaches: the object passes this kernel's
// verifier (CO-RE relocations against the runner's BTF included), the
// tracepoint attaches, Attach is idempotent, and the maps are readable.
func TestExecWatch_RealObjectLoadsAndAttaches(t *testing.T) {
	w := loadAttached(t)
	if err := w.Attach(); err != nil {
		t.Fatalf("second Attach (idempotency): %v", err)
	}
	emitted, lost, err := w.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	t.Logf("fresh object: emitted=%d lost=%d", emitted, lost)
	if err := w.WatchCgroup(1, 1); err != nil {
		t.Fatalf("WatchCgroup: %v", err)
	}
	if err := w.UnwatchCgroup(1); err != nil {
		t.Fatalf("UnwatchCgroup: %v", err)
	}
	if err := w.UnwatchCgroup(1); err != nil {
		t.Fatalf("UnwatchCgroup (already gone): %v", err)
	}
}

// TestExecWatch_ShippedObjectSubmitsWithoutWakeup guards the kernel half of
// #2774, which no timing in the lane would catch (a timer consumer passes
// every overhead assertion even if the program woke it per record): the
// object as built ships exec_wakeup_per_record = 0, so the program submits
// with BPF_RB_NO_WAKEUP.
func TestExecWatch_ShippedObjectSubmitsWithoutWakeup(t *testing.T) {
	spec, err := ebpf.LoadCollectionSpec(ebpfLoadExecObjPath)
	if err != nil {
		t.Fatalf("LoadCollectionSpec: %v", err)
	}
	v, ok := spec.Variables["exec_wakeup_per_record"]
	if !ok {
		t.Fatal("object has no exec_wakeup_per_record variable")
	}
	var perRecord uint8
	if err := v.Get(&perRecord); err != nil {
		t.Fatalf("read exec_wakeup_per_record: %v", err)
	}
	if perRecord != 0 {
		t.Fatalf("exec_wakeup_per_record defaults to %d in the shipped object, want 0 (BPF_RB_NO_WAKEUP)", perRecord)
	}
	if ms := spec.Maps[mapExecEvents]; ms == nil || ms.MaxEntries != execRingBytes {
		t.Fatalf("exec_events map spec = %+v, want a %d-byte ring", ms, execRingBytes)
	}
}

// TestExecWatch_WatchedCgroupYieldsExactlyOneEvent is the design's filtering
// contract on a real kernel: a host exec emits nothing; `sh -c true` inside
// a registered cgroup emits exactly one event carrying that cgroup id (the
// shell's own exec — `true` is a shell builtin, so no second exec).
func TestExecWatch_WatchedCgroupYieldsExactlyOneEvent(t *testing.T) {
	w := loadAttached(t)
	sink := startConsumer(t, w)
	cg := newTestCgroup(t, "one")

	// Host exec before anything is watched: nothing.
	runExec(t, nil, "sh", "-c", "true")
	time.Sleep(300 * time.Millisecond)
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("host exec emitted %d event(s) with nothing watched: %+v", len(got), got)
	}

	if err := w.WatchCgroup(cg.id, 1); err != nil {
		t.Fatalf("WatchCgroup(%d): %v", cg.id, err)
	}
	runExec(t, cg, "sh", "-c", "true")
	// Another host exec after the watch: still filtered.
	runExec(t, nil, "sh", "-c", "true")

	var got []ExecEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got = sink.snapshot()
		if len(got) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // let a spurious second event, if any, arrive
	got = sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("watched exec yielded %d event(s), want exactly 1: %+v", len(got), got)
	}
	ev := got[0]
	t.Logf("event: pid=%d ppid=%d uid=%d cgroup=%d comm=%q filename=%q argv=%q truncated=%v",
		ev.HostPID, ev.PPID, ev.UID, ev.CgroupID, ev.Comm, ev.Filename, ev.Argv, ev.ArgvTruncated)
	if ev.CgroupID != cg.id {
		t.Errorf("cgroup_id = %d, want %d (inode of %s)", ev.CgroupID, cg.id, cg.path)
	}
	if ev.Comm != "sh" {
		t.Errorf("comm = %q, want sh", ev.Comm)
	}
	if !strings.HasSuffix(ev.Filename, "/sh") {
		t.Errorf("filename = %q, want a path ending in /sh", ev.Filename)
	}
	if strings.Join(ev.Argv, " ") != "sh -c true" {
		t.Errorf("argv = %q, want [sh -c true]", ev.Argv)
	}
	if ev.ArgvTruncated {
		t.Errorf("argv_truncated set for a 10-byte argv")
	}
	if ev.PPID != uint32(os.Getpid()) {
		t.Errorf("ppid = %d, want the test process %d", ev.PPID, os.Getpid())
	}
	if ev.UID != uint32(os.Getuid()) {
		t.Errorf("uid = %d, want %d", ev.UID, os.Getuid())
	}
	for _, a := range ev.Argv {
		if strings.HasPrefix(a, "PATH=") || strings.HasPrefix(a, "HOME=") {
			t.Errorf("argv carries an environment variable: %q", a)
		}
	}
}

// TestExecWatch_LongArgvIsCappedAndFlagged: an argv past 512 bytes arrives
// truncated with the flag set, and never grows into the environment.
func TestExecWatch_LongArgvIsCappedAndFlagged(t *testing.T) {
	w := loadAttached(t)
	sink := startConsumer(t, w)
	cg := newTestCgroup(t, "long")
	if err := w.WatchCgroup(cg.id, 1); err != nil {
		t.Fatalf("WatchCgroup: %v", err)
	}
	long := strings.Repeat("x", 700)
	runExec(t, cg, "sh", "-c", "true", "sh", long)
	var got []ExecEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got = sink.snapshot(); len(got) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ev := got[0]
	if !ev.ArgvTruncated {
		t.Errorf("argv_truncated not set for a %d-byte argv", len("sh\x00-c\x00true\x00sh\x00")+len(long))
	}
	if n := len(ev.ArgvString()); n > execArgvLen {
		t.Errorf("argv string is %d bytes, cap is %d", n, execArgvLen)
	}
	if len(ev.Argv) < 4 || !strings.HasPrefix(ev.Argv[len(ev.Argv)-1], "xxxx") {
		t.Errorf("argv = %q, want the long argument's head as the last element", ev.Argv)
	}
}

// TestExecWatch_BurstReportsLostNotPanic: 10k execs in a watched cgroup with
// nobody draining the ring (4 MiB, ~5,090 records). The kernel must count the overflow in
// exec_stats[lost] (reported as exec_events_lost) and keep running; the
// ring must still be readable afterwards.
func TestExecWatch_BurstReportsLostNotPanic(t *testing.T) {
	w := loadAttached(t)
	cg := newTestCgroup(t, "burst")
	if err := w.WatchCgroup(cg.id, 1); err != nil {
		t.Fatalf("WatchCgroup: %v", err)
	}
	const n = 10000
	start := time.Now()
	runExec(t, cg, "sh", "-c", fmt.Sprintf("i=0; while [ $i -lt %d ]; do /bin/true; i=$((i+1)); done", n))
	elapsed := time.Since(start)

	emitted, lost, err := w.Stats()
	if err != nil {
		t.Fatalf("Stats after burst: %v", err)
	}
	ringBytes := w.EventsMap().MaxEntries()
	// Each record takes the 8-byte ringbuf header plus the 816-byte sample
	// (already 8-aligned), so an undrained ring holds exactly this many.
	capacity := uint64(ringBytes) / (execEventSize + 8)
	t.Logf("burst of %d execs in %s: emitted=%d lost=%d (ring %d bytes, %d-byte records, capacity %d)", n, elapsed.Round(time.Millisecond), emitted, lost, ringBytes, execEventSize, capacity)
	if ringBytes != execRingBytes {
		t.Errorf("exec_events ring is %d bytes, want %d (execRingBytes)", ringBytes, execRingBytes)
	}
	if emitted != capacity {
		t.Errorf("emitted = %d into an undrained %d-byte ring, want its capacity %d", emitted, ringBytes, capacity)
	}
	if emitted+lost < n {
		t.Fatalf("emitted+lost = %d, want >= %d (every watched exec is either emitted or counted lost)", emitted+lost, n)
	}
	if lost == 0 {
		t.Fatalf("lost = 0 after %d undrained execs into a %d-byte ring — the overflow counter is not working", n, ringBytes)
	}
	// The ring is still usable: a reader drains the records that did fit.
	rd, err := w.NewExecReader()
	if err != nil {
		t.Fatalf("NewExecReader after burst: %v", err)
	}
	defer func() { _ = rd.Close() }()
	rec, err := rd.Read()
	if err != nil {
		t.Fatalf("Read after burst: %v", err)
	}
	ev, err := ParseExecEvent(rec.RawSample)
	if err != nil {
		t.Fatalf("ParseExecEvent after burst: %v", err)
	}
	if ev.CgroupID != cg.id {
		t.Errorf("post-burst record cgroup_id = %d, want %d", ev.CgroupID, cg.id)
	}
}

// The overhead measurement (#2774) runs its exec load from a SEPARATE
// process, the shape the daemon sees in production: the exec'ing tasks live
// in a box, the consumer in an otherwise idle daemon. The first version of
// this test (#2637) generated the load from 16 goroutines in the test
// process itself and measured the kernel side as the RUSAGE_CHILDREN delta.
// That under-reported a per-event-wakeup design ~19x against a KVM guest:
// the consumer's wakeups landed in a process whose threads were already
// awake forking, so a wakeup cost little and drowned in the generator's own
// fork cost (subtracted as a noisy baseline); and the kernel-side cost of
// waking the consumer (irq_work, the IPI to an idle CPU) is not charged to
// the exec'ing child at all. Now the generator is this test binary re-exec'd
// with execLoadGenEnv set, the kernel side is the program's own run time
// (BPF_STATS_RUN_TIME), and the consumer side is this process's CPU — the
// same split the guest measurement used (bpftool run_time_ns + the daemon's
// /proc/<pid>/stat utime+stime).
const execLoadGenEnv = "EXECWATCH_LOADGEN" // "<rate>:<seconds>"

func init() {
	v := os.Getenv(execLoadGenEnv)
	if v == "" {
		return
	}
	var rate, secs int
	if _, err := fmt.Sscanf(v, "%d:%d", &rate, &secs); err != nil || rate <= 0 || secs <= 0 {
		fmt.Fprintf(os.Stderr, "loadgen: bad %s=%q\n", execLoadGenEnv, v)
