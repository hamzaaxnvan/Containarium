//go:build linux

package server

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/threatdetect"
)

// BenchmarkExecCapture measures what the exec ring consumer pays per exec to
// capture the identity the kill checks (#2765 follow-up):
//
//   - procstat-self / procstat-child: the #2867 design, reading
//     /proc/<pid>/stat field 22 per exec (open, read, close on procfs plus
//     the kernel rendering the line). "child" is a single-threaded process,
//     like most exec'd commands; "self" is this multi-threaded test binary.
//   - bootclock: the stamp the watcher takes instead, clock_gettime
//     (CLOCK_BOOTTIME).
//   - time-now: time.Now, the vDSO clock read the watcher already pays per
//     exec (ExecEventRecord.At), for reference.
//
// Run on a Linux host or guest (procfs is Linux only):
//
//	go test -run '^$' -bench 'BenchmarkExecCapture|BenchmarkExecWatcherOnExecEvent' \
//	  -benchtime 200000x -count 3 ./internal/server/
func BenchmarkExecCapture(b *testing.B) {
	b.Run("procstat-self", func(b *testing.B) {
		pid := uint32(os.Getpid()) // #nosec G115 -- a live pid fits
		benchProcStat(b, pid)
	})
	b.Run("procstat-child", func(b *testing.B) {
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			b.Skipf("no sleep binary: %v", err)
		}
		defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
		benchProcStat(b, uint32(cmd.Process.Pid)) // #nosec G115 -- a live pid fits
	})
	b.Run("bootclock", func(b *testing.B) {
