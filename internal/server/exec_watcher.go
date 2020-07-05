package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf/ringbuf"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// Host paths the watcher resolves a container's cgroup id from. Fields on the
// watcher so tests point them at a fake tree.
const (
	execProcRoot   = "/proc"
	execCgroupRoot = "/sys/fs/cgroup"

	// execReplayRing is how many recent events the watcher keeps in memory
	// for StreamExecEventsRequest.since. Events are deliberately NOT written
	// to the audit log (only matched findings are); this ring is the only
	// history and it is bounded.
	execReplayRing = 4096
	// execSubscriberBuffer is the per-stream channel depth. A subscriber
	// that lets it fill is dropped (its channel closed) rather than ever
	// blocking the ring consumer. Subscribe raises it to the replay's length
	// so a `--since … --follow` stream has room for the live events that
	// arrive while its replay is being sent.
	execSubscriberBuffer = 256

	execTelemetryOK               = "ok"
	execTelemetryNotStartedStatus = "unavailable (not started)"
	execTelemetryStoppedStatus    = "unavailable (stopped)"
)

// execTelemetryUnavailable renders a load/attach failure as the status
// string GetSentryStatus.exec_telemetry carries and the health loop keys on.
func execTelemetryUnavailable(err error) string { return "unavailable (" + err.Error() + ")" }

// execCgroupMap is what the watcher needs from the kernel object:
// *netbpf.ExecWatch satisfies it; tests use a fake.
type execCgroupMap interface {
	WatchCgroup(cgroupID uint64, index uint32) error
	UnwatchCgroup(cgroupID uint64) error
}

// execTarget is the container a watched cgroup id maps back to.
type execTarget struct {
	Container string
	TenantID  string
}

// ExecEventRecord is one decoded, attributed exec event — what the engine
// (as a threatdetect.ExecEvent), the replay ring and the stream subscribers
// see.
type ExecEventRecord struct {
	Container string
	TenantID  string
	At        time.Time
	Event     netbpf.ExecEvent
}

type execSubscriber struct {
	container string
	ch        chan ExecEventRecord
}

// ExecWatcher is the userspace half of the exec telemetry (#2637; design
// component 1): it keeps the kernel's watched_cgroups map in step with the
// running tenant containers, attributes each ring event to its container,
// hands it to the threat-detection engine and to a bounded fan-out for
// StreamExecEvents, and reports its own health as the `exec-telemetry`
// status line. It rides the NetworkPolicyEnforcer's 10 s reconcile (which
// already lists the containers with their init pids) and its lifecycle, but
// loads its own object: a failure here leaves the flow rules untouched.
type ExecWatcher struct {
	procRoot   string
	cgroupRoot string
	engine     func(threatdetect.ExecEvent) // nil = no engine consumer
	now        func() time.Time
	// bootTicks stamps each decoded exec (bootClockTicks; a fake in tests).
	bootTicks func() (threatdetect.ProcStartTicks, error)

	mu           sync.Mutex
	status       string
	maps         execCgroupMap // nil until Start loaded the object
	lost         func() uint64
	byCgroup     map[uint64]execTarget
	byName       map[string]uint64
	resolveFail  map[string]string // container → last logged resolve error, to log once
	nextIndex    uint32
	ring         []ExecEventRecord // replay ring, oldest at ringHead
	ringHead     int
	subs         map[int]*execSubscriber
	nextSub      int
	unattributed uint64
	subsDropped  uint64
	stampFails   uint64 // boot-clock reads that failed (see noteStampFailure)

	watch  *netbpf.ExecWatch
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewExecWatcher builds a watcher that hands attributed events to engine
// (nil when no rule consumes them). Start loads the kernel object.
func NewExecWatcher(engine func(threatdetect.ExecEvent)) *ExecWatcher {
	return &ExecWatcher{
		procRoot:    execProcRoot,
		cgroupRoot:  execCgroupRoot,
		engine:      engine,
		now:         time.Now,
		bootTicks:   bootClockTicks,
		status:      execTelemetryNotStartedStatus,
		byCgroup:    make(map[uint64]execTarget),
		byName:      make(map[string]uint64),
		resolveFail: make(map[string]string),
		subs:        make(map[int]*execSubscriber),
	}
}

// Start loads the execwatch object from the same operator value the
// enforcer resolves (netbpf.ResolveExecWatch), attaches the tracepoint and
// starts the ring consumer. On any failure the status becomes
// `unavailable (<reason>)` and the error is returned; the caller logs and
// keeps the flow rules running.
func (w *ExecWatcher) Start(ctx context.Context, objValue string) error {
	watch, err := netbpf.ResolveExecWatch(objValue)
	if err != nil {
		w.setStatus(execTelemetryUnavailable(err))
		return err
	}
	if err := watch.Attach(); err != nil {
		_ = watch.Close()
		w.setStatus(execTelemetryUnavailable(err))
		return err
	}
	rd, err := watch.NewExecReader()
	if err != nil {
		_ = watch.Close()
		w.setStatus(execTelemetryUnavailable(err))
		return err
	}
	cctx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.watch = watch
	w.maps = watch
	w.lost = watch.Lost
	w.cancel = cancel
	w.status = execTelemetryOK
	w.mu.Unlock()

	w.wg.Add(1)
	go w.consume(cctx, rd)
	return nil
}

// execRingReader is the ring reader the consumer drains: *ringbuf.Reader in
// the daemon, a fake in tests. The consumer drains it in timed batches
// (netbpf.ExecDrainInterval, #2774), hence ReadInto, SetDeadline and
// AvailableBytes.
type execRingReader interface {
	ReadInto(rec *ringbuf.Record) error
	SetDeadline(t time.Time)
	AvailableBytes() int
	Close() error
}

// consume runs the ring consumer until ctx ends or the reader fails. A
// consumer that exits while the watcher is still meant to be running (a
// read error, not Stop) is a blind spot — the kernel keeps emitting into a
// ring nobody drains — so the status flips to `unavailable (ring consumer
// exited: …)`, which GetSentryStatus reports and the health loop raises,
// and every open stream is ended rather than left waiting forever.
func (w *ExecWatcher) consume(ctx context.Context, rd execRingReader) {
	defer w.wg.Done()
	// Close only after the loop has returned. The loop never blocks longer
	// than one drain tick (its reads have a deadline of now), so a cancelled
	// ctx ends it promptly, and closing concurrently would race the reader's
	// unlocked AvailableBytes (cilium/ebpf unmaps the ring in Close).
	defer func() { _ = rd.Close() }()
	var last error
	netbpf.ConsumeExecEvents(ctx, rd, w, func(err error) {
		log.Printf("[exec-telemetry] ring: %v", err)
		last = err
	})
	if ctx.Err() != nil {
		return // Stop: it owns the status
	}
	reason := errors.New("ring consumer exited")
	if last != nil {
		reason = fmt.Errorf("ring consumer exited: %w", last)
	}
	log.Printf("[exec-telemetry] %v; exec telemetry is unavailable until the daemon restarts", reason)
	w.mu.Lock()
	w.status = execTelemetryUnavailable(reason)
	w.closeSubscribersLocked()
	w.mu.Unlock()
}

// Stop ends the consumer, detaches the tracepoint and frees the object. It
// ends every open stream (closes its channel after the status has left
// "ok", so StreamExecEvents returns UNAVAILABLE rather than holding up the
// gRPC server's GracefulStop) and forgets the attribution tables, so a
// later Start re-registers every box in the new kernel map.
func (w *ExecWatcher) Stop() {
	w.mu.Lock()
	cancel, watch := w.cancel, w.watch
	w.cancel, w.watch, w.maps, w.lost = nil, nil, nil, nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
	if watch != nil {
		if err := watch.Close(); err != nil {
			log.Printf("[exec-telemetry] close: %v", err)
		}
	}
	w.mu.Lock()
	if watch != nil || w.status == execTelemetryOK {
		w.status = execTelemetryStoppedStatus
	}
	w.closeSubscribersLocked()
	w.byCgroup = make(map[uint64]execTarget)
	w.byName = make(map[string]uint64)
	w.resolveFail = make(map[string]string)
	w.mu.Unlock()
}

// closeSubscribersLocked ends every stream subscription. Callers hold w.mu
// and have already moved the status off "ok", which is how the stream
// handler tells this apart from a slow subscriber being dropped.
func (w *ExecWatcher) closeSubscribersLocked() {
	for id, s := range w.subs {
		close(s.ch)
		delete(w.subs, id)
	}
}

func (w *ExecWatcher) setStatus(s string) {
	w.mu.Lock()
	w.status = s
	w.mu.Unlock()
}

// Status is the GetSentryStatus.exec_telemetry string: "ok" or
// "unavailable (<reason>)".
func (w *ExecWatcher) Status() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// Running reports whether the object is attached and events can flow.
func (w *ExecWatcher) Running() bool { return w.Status() == execTelemetryOK }

// Lost is the kernel's count of events dropped because the ring was full
// (GetSentryStatus.exec_events_lost); 0 before Start.
func (w *ExecWatcher) Lost() int64 {
	w.mu.Lock()
	fn := w.lost
	w.mu.Unlock()
	if fn == nil {
		return 0
	}
	v := fn()
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// Unattributed counts ring events whose cgroup id matched no registered
// container (a box that stopped between the exec and the decode, or a stale
// map entry); they are dropped, never guessed.
func (w *ExecWatcher) Unattributed() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.unattributed
}

// SubscribersDropped counts stream subscribers disconnected for not keeping up.
func (w *ExecWatcher) SubscribersDropped() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.subsDropped
}

func (w *ExecWatcher) lookup(cgroupID uint64) (execTarget, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.byCgroup[cgroupID]
	return t, ok
}

func (w *ExecWatcher) watchedCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.byName)
}

// execDesired is one running tenant container the reconcile wants watched.
type execDesired struct {
	id     uint64
	tenant string
}

// Reconcile converges watched_cgroups with the running tenant containers:
// each running, non-core container with an init pid is resolved
// /proc/<pid>/cgroup → the container's root cgroup → its inode and
// upserted; a container whose id changed (restart: new init, new cgroup
// directory) has the old id removed; stopped or vanished containers are
// removed. Core-role containers (control plane, postgres, caddy, …) are
