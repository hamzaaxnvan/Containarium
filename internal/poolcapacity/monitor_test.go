package poolcapacity

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		used, total int64
		want        Level
	}{
		{0, 0, LevelUnknown},
		{849, 1000, LevelOK},
		{850, 1000, LevelWarning},
		{949, 1000, LevelWarning},
		{950, 1000, LevelCritical},
		{1000, 1000, LevelCritical},
	} {
		if got := Classify(tc.used, tc.total); got != tc.want {
			t.Errorf("Classify(%d, %d) = %v, want %v", tc.used, tc.total, got, tc.want)
		}
	}
}

// fakeSource is a stubbed pool reader: it returns pools/err, optionally after
// blocking until release is closed.
type fakeSource struct {
	mu      sync.Mutex
	pools   []incus.StoragePoolUsage
	err     error
	release chan struct{}
}

func (f *fakeSource) set(pools []incus.StoragePoolUsage, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pools, f.err = pools, err
}

func (f *fakeSource) read() ([]incus.StoragePoolUsage, error) {
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pools, f.err
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func pool(name string, used int64) incus.StoragePoolUsage {
	return incus.StoragePoolUsage{Name: name, Driver: "zfs", UsedBytes: used, TotalBytes: 100}
}

func newTestMonitor(src *fakeSource, logs *logSink, onSample func([]Sample)) *Monitor {
	return New(src.read, Options{Timeout: 50 * time.Millisecond, OnSample: onSample, Logf: logs.logf})
}

// A pool above warn produces the series and one WARN at the first read (the
// daemon start); below warn produces the series and no WARN.
func TestMonitor_WarnAndCritProduceSeriesAndLog(t *testing.T) {
	for _, tc := range []struct {
		used      int64
		wantLevel Level
