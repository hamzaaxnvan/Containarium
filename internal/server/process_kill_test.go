package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeProc writes /proc/<pid>/cgroup files under a temp root, and a
// /proc/<pid>/stat whose start time (field 22) is fakeStart(pid).
func fakeProc(t *testing.T, cgroups map[int]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, path := range cgroups {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::"+path+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeStat(t, root, pid, "box proc", fakeStart(pid))
	}
	return root
}

// fakeStart is the start time fakeProc gives pid: distinct per pid.
func fakeStart(pid int) threatdetect.ProcStartTicks {
	return threatdetect.ProcStartTicks(1_000_000 + pid) // #nosec G115 -- small test pids
}

// statLine renders a /proc/<pid>/stat line with start (field 22) set.
func statLine(pid int, comm string, start threatdetect.ProcStartTicks) string {
	// Fields 3..21: state ppid pgrp session tty_nr tpgid flags minflt
	// cminflt majflt cmajflt utime stime cutime cstime priority nice
	// num_threads itrealvalue; then 22 starttime, 23 vsize, 24 rss.
	return fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 4194560 10 0 0 0 7 3 0 0 20 0 1 0 %d 1234567 89\n", pid, comm, pid, pid, start)
}

func writeStat(t *testing.T, root string, pid int, comm string, start threatdetect.ProcStartTicks) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statLine(pid, comm, start)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestParseProcStatStartTime: field 22 is counted from the LAST ')', so a
// comm with spaces, parentheses or a ") S 1 2" lookalike cannot shift it.
func TestParseProcStatStartTime(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    threatdetect.ProcStartTicks
		wantErr bool
	}{
		{name: "plain", in: statLine(4242, "xmrig", 987654), want: 987654},
		{name: "comm with spaces", in: statLine(4242, "my miner", 55), want: 55},
		{name: "comm with parens", in: statLine(4242, "a) (b", 66), want: 66},
		{name: "comm forging fields", in: statLine(4242, "x) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 999", 77), want: 77},
		{name: "no closing paren", in: "4242 (xmrig S 1 2 3", wantErr: true},
		{name: "truncated", in: "4242 (xmrig) S 1 4242 4242 0 -1", wantErr: true},
		{name: "not a number", in: "4242 (xmrig) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 abc 1", wantErr: true},
		{name: "zero is not a start time", in: statLine(4242, "xmrig", 0), wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProcStatStartTime([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("got %d, want an error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

// TestHostProcessKiller_StartTimeCheck (#2765): after the pin and the cgroup
// re-check, the kill reads the pinned pid's start time and compares it with
// the boot-clock stamp the exec watcher put on the matched exec. The matched
// process started before its exec was stamped and is killed; a pid reused
// by another process of the SAME box (same cgroup, started after the stamp)
// is refused as pid reused, with both times in the error the audit row
// carries; an exec with no stamp, or a pid whose stat cannot be read or
// parsed, is never signalled (fail closed); a pid gone by then is gone.
func TestHostProcessKiller_StartTimeCheck(t *testing.T) {
	const initPID, pid = 100, 4242
	live := fakeStart(pid) // the start time fakeProc gives pid
	tests := []struct {
		name     string
		stamp    threatdetect.ProcStartTicks // the matched exec's
		stat     string                      // "" = fakeProc's line; "-" = no stat file
		wantErr  error                       // nil = killed
		wantText []string
	}{
		{name: "stamped in the tick it started is killed", stamp: live},
		{name: "stamped after it started is killed", stamp: live + 500},
		{name: "same-box pid reuse is refused", stamp: live - 500, wantErr: errKillPIDReused,
			wantText: []string{fmt.Sprintf("started at %d ticks", live), fmt.Sprintf("seen at %d", live-500), "pid reused"}},
		{name: "no stamp is refused", stamp: 0, wantErr: errKillStartTimeUnknown},
		{name: "stat gone after the pin", stamp: live, stat: "-", wantErr: errKillProcessGone},
		{name: "unparsable stat fails closed", stamp: live, stat: "4242 (xmrig S 1", wantErr: errKillStartTimeUnknown},
		{name: "zero start time fails closed", stamp: live, stat: statLine(pid, "xmrig", 0), wantErr: errKillStartTimeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fakeProc(t, map[int]string{
				initPID: "/lxc.payload.box-a/init.scope",
				pid:     "/lxc.payload.box-a/system.slice/ssh.service",
			})
			statPath := filepath.Join(root, strconv.Itoa(pid), "stat")
			switch tt.stat {
			case "":
			case "-":
				if err := os.Remove(statPath); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(statPath, []byte(tt.stat), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var killed []int
			opened := 0
			k := &hostProcessKiller{
				procRoot: root,
				initPID:  func(string) (int64, error) { return initPID, nil },
				open: func(p int) (pidHandle, error) {
					opened++
					return fakePIDHandle{killed: &killed, pid: p}, nil
				},
			}
			err := k.KillInContainer("box-a", pid, tt.stamp)
			if tt.wantErr == nil {
				if err != nil || len(killed) != 1 || killed[0] != pid {
					t.Fatalf("err=%v killed=%v, want pid %d killed", err, killed, pid)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(killed) != 0 {
				t.Fatalf("signalled %v on a refused kill", killed)
			}
			for _, w := range tt.wantText {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("err %q does not contain %q (the audit row needs both times)", err, w)
				}
			}
			if tt.wantErr == errKillPIDReused && opened != 1 {
				t.Fatalf("pidfd opened %d times; the start time must be compared after the pin", opened)
			}
		})
	}
}

// TestHostProcessKiller_StampToleranceBoundary: with a fake stat source, a
// process that started up to one tick after the stamp is still the matched
// one (clock skew between CPUs); two ticks later is refused. A process that
// started before the stamp is always killed. The tick values are literal on
// purpose: a slack of 0 or 2 fails this test.
func TestHostProcessKiller_StampToleranceBoundary(t *testing.T) {
	const initPID, pid, stamp = 100, 4242, threatdetect.ProcStartTicks(500_000)
	tests := []struct {
		start threatdetect.ProcStartTicks
		kill  bool
	}{
		{start: 1, kill: true},
		{start: stamp - 1, kill: true},
		{start: stamp, kill: true},
		{start: stamp + 1, kill: true},
		{start: stamp + 2, kill: false},
		{start: stamp + 100_000, kill: false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("start=%d", tt.start), func(t *testing.T) {
			withProcStartReader(t, func(string, uint32) (threatdetect.ProcStartTicks, error) { return tt.start, nil })
			var killed []int
			k := &hostProcessKiller{
				procRoot: fakeProc(t, map[int]string{initPID: "/lxc.payload.box-a/init.scope", pid: "/lxc.payload.box-a/user.slice"}),
				initPID:  func(string) (int64, error) { return initPID, nil },
				open:     func(p int) (pidHandle, error) { return fakePIDHandle{killed: &killed, pid: p}, nil },
			}
			err := k.KillInContainer("box-a", pid, stamp)
			if tt.kill != (err == nil && len(killed) == 1) {
				t.Fatalf("err=%v killed=%v, want kill=%v", err, killed, tt.kill)
			}
			if !tt.kill && !errors.Is(err, errKillPIDReused) {
				t.Fatalf("err = %v, want pid reused", err)
			}
		})
	}
}

// withProcStartReader swaps the kill's stat reader for the test. Tests that
// use it must not run in parallel.
func withProcStartReader(t *testing.T, f func(string, uint32) (threatdetect.ProcStartTicks, error)) {
	t.Helper()
	prev := readProcStart
	readProcStart = f
	t.Cleanup(func() { readProcStart = prev })
}

// TestBootTicksOf: the stamp is floored to USER_HZ ticks exactly as the
// kernel renders /proc/<pid>/stat field 22 (nsec_to_clock_t).
func TestBootTicksOf(t *testing.T) {
	tests := []struct {
		ns   int64
		hz   uint64
		want threatdetect.ProcStartTicks
	}{
		{ns: 12_345_678_901, hz: 100, want: 1234},
		{ns: 9_999_999, hz: 100, want: 0},
		{ns: 10_000_000, hz: 100, want: 1},
		{ns: 12_345_678_901, hz: 1000, want: 12345},
		{ns: 0, hz: 100, want: 0},
		{ns: -5, hz: 100, want: 0},
		{ns: 5, hz: 0, want: 0},
	}
	for _, tt := range tests {
		if got := bootTicksOf(tt.ns, tt.hz); got != tt.want {
			t.Errorf("bootTicksOf(%d, %d) = %d, want %d", tt.ns, tt.hz, got, tt.want)
		}
	}
}

type fakePIDHandle struct {
	killed *[]int
	pid    int
	err    error
}

func (h fakePIDHandle) Kill() error {
	if h.err != nil {
		return h.err
	}
	*h.killed = append(*h.killed, h.pid)
	return nil
}
func (fakePIDHandle) Close() error { return nil }

// TestHostProcessKiller_CgroupRecheck: the kill reaches a pid only while it
// is still under the container's cgroup; a reused pid (another box, the
// host), the container's init, and a pid that is gone are all refused
// without a signal.
func TestHostProcessKiller_CgroupRecheck(t *testing.T) {
	const initPID = 100
	proc := fakeProc(t, map[int]string{
		initPID: "/lxc.payload.box-a/init.scope",
		4242:    "/lxc.payload.box-a/system.slice/ssh.service",
		5000:    "/lxc.payload.box-b/user.slice",
		6000:    "/system.slice/cron.service",
	})
	tests := []struct {
		name    string
		pid     uint32
		openErr error
		killErr error
		wantErr error // nil = killed
	}{
		{name: "same container is killed", pid: 4242},
		{name: "pid now in another box", pid: 5000, wantErr: errKillPIDReused},
		{name: "pid now a host process", pid: 6000, wantErr: errKillPIDReused},
		{name: "container init is never killed", pid: initPID, wantErr: errKillContainerInit},
		{name: "pid vanished before the re-check", pid: 7777, wantErr: errKillProcessGone},
		{name: "pid exited before the open", pid: 4242, openErr: errKillProcessGone, wantErr: errKillProcessGone},
		{name: "exited between check and signal", pid: 4242, killErr: errKillProcessGone, wantErr: errKillProcessGone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var killed []int
			k := &hostProcessKiller{
				procRoot: proc,
				initPID:  func(string) (int64, error) { return initPID, nil },
				open: func(pid int) (pidHandle, error) {
					if tt.openErr != nil {
						return nil, tt.openErr
					}
					return fakePIDHandle{killed: &killed, pid: pid, err: tt.killErr}, nil
				},
			}
			err := k.KillInContainer("box-a", tt.pid, fakeStart(int(tt.pid)))
			if tt.wantErr == nil {
				if err != nil || len(killed) != 1 || killed[0] != int(tt.pid) {
					t.Fatalf("err=%v killed=%v, want pid %d killed", err, killed, tt.pid)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(killed) != 0 {
				t.Fatalf("signalled %v on a refused kill", killed)
			}
		})
	}
}

// TestHostProcessKiller_RefusesNonIncusRuntime (#2768 item 1, decided on
// #2781: refuse up front): when the container's init does not sit under an
// Incus payload cgroup ("lxc.payload.<name>"), the cgroup re-check has no
// container root to compare against and would refuse every kill as "pid
// reused". The kill is refused before anything is pinned or compared, with
// errKillRuntimeUnsupported, whatever the pid or its start time.
func TestHostProcessKiller_RefusesNonIncusRuntime(t *testing.T) {
	const initPID = 100
	for name, cgroups := range map[string]map[int]string{
		"systemd box, init.scope at the root": {initPID: "/init.scope", 4242: "/system.slice/app.service"},
		"other runtime's slice":               {initPID: "/machine.slice/box-a.scope/init.scope", 4242: "/machine.slice/box-a.scope/system.slice/app.service"},
		"same cgroup as init":                 {initPID: "/docker/abc123", 4242: "/docker/abc123"},
	} {
		t.Run(name, func(t *testing.T) {
			proc := fakeProc(t, cgroups)
			for _, start := range []threatdetect.ProcStartTicks{fakeStart(4242), 0} {
				k := &hostProcessKiller{
					procRoot: proc,
					initPID:  func(string) (int64, error) { return initPID, nil },
					open: func(int) (pidHandle, error) {
						t.Fatal("pinned a pid on a non-Incus runtime; the refusal must come first")
						return nil, nil
					},
				}
				err := k.KillInContainer("box-a", 4242, start)
				if !errors.Is(err, errKillRuntimeUnsupported) || errors.Is(err, errKillPIDReused) {
					t.Fatalf("start %d: err = %v, want errKillRuntimeUnsupported", start, err)
				}
				if !strings.Contains(err.Error(), "kill_process is Incus-only") {
					t.Fatalf("err = %v, want the reason to say kill_process is Incus-only", err)
				}
			}
		})
	}
}

func TestHostProcessKiller_RefusesLowPIDsAndStoppedBox(t *testing.T) {
	k := &hostProcessKiller{procRoot: t.TempDir(),
		initPID: func(string) (int64, error) { return 0, nil },
		open:    func(int) (pidHandle, error) { t.Fatal("must not open"); return nil, nil }}
	for _, pid := range []uint32{0, 1} {
		if err := k.KillInContainer("box-a", pid, fakeStart(int(pid))); err == nil {
			t.Errorf("pid %d: kill not refused", pid)
		}
	}
	if err := k.KillInContainer("box-a", 4242, fakeStart(4242)); !errors.Is(err, errKillProcessGone) {
		t.Errorf("stopped box: err = %v, want errKillProcessGone", err)
	}
	k.initPID = func(string) (int64, error) { return 0, errors.New("incus down") }
	if err := k.KillInContainer("box-a", 4242, fakeStart(4242)); err == nil {
		t.Error("init pid lookup failure must refuse the kill")
	}
