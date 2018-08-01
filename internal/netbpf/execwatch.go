package netbpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
)

// Names in experimental/ebpf-phaseA/execwatch.bpf.c (#2637) — the second BPF
// object, kept apart from netpolicy.bpf.o so a verifier rejection or missing
// BTF here never takes flow enforcement down.
const (
	progExecWatch       = "execwatch"
	mapWatchedCgroups   = "watched_cgroups"
	mapExecEvents       = "exec_events"
	mapExecStats        = "exec_stats"
	execStatEmitted     = uint32(0)
	execStatLost        = uint32(1)
	execTracepointGroup = "sched"
	execTracepointName  = "sched_process_exec"

	// ExecObjectName is the exec object's file name; next to a netpolicy
	// object path it is the sibling ResolveExecWatch loads.
	ExecObjectName = "execwatch.bpf.o"
)

// Wire layout of `struct exec_event` — see the table in execwatch.bpf.c.
// TestExecEventWireLayout pins these against a Go mirror of the C struct.
const (
	execCommLen     = 16
	execFilenameLen = 256
	execArgvLen     = 512

	execOffHostPID       = 0
	execOffPPID          = 4
	execOffUID           = 8
	execOffCgroupID      = 16
	execOffComm          = 24
	execOffFilename      = 40
	execOffArgv          = 296
	execOffArgvTruncated = 808
	// execEventMin is the shortest sample that carries every field; the C
	// struct's tail padding brings sizeof to execEventSize. The parser accepts
	// anything from execEventMin up so a newer object that appends fields still
	// decodes on this daemon.
	execEventMin  = execOffArgvTruncated + 1
	execEventSize = 816

	// execRingBytes is EXEC_RING_BYTES in execwatch.bpf.c: 4 MiB, ~5,090
	// records of 816 bytes plus the 8-byte ring header (#2774 review). The
	// lane checks the compiled map against it.
	execRingBytes = 4 << 20
)

// ExecEvent is the decoded form of one `struct exec_event` the execwatch
// program emitted for a process exec inside a watched cgroup.
type ExecEvent struct {
	HostPID  uint32 // tgid in the host pid namespace
	PPID     uint32 // parent tgid, host pid namespace
	UID      uint32 // real uid of the exec'ing task
	CgroupID uint64 // the watched_cgroups key that matched — maps back to the container
	Comm     string // task comm after exec
	Filename string // resolved executable path from the tracepoint
	// Argv is the argument vector split on NUL. When ArgvTruncated is set the
	// block was capped at 512 bytes and the last element may be partial.
	Argv          []string
	ArgvTruncated bool
}

// ArgvString joins Argv with single spaces, for logs and the CLI line.
func (e ExecEvent) ArgvString() string { return strings.Join(e.Argv, " ") }

// ParseExecEvent decodes one ring-buffer sample into an ExecEvent. It
// tolerates a sample longer than the struct (a newer object appending fields)
// and rejects a short one. comm and filename are read up to their first NUL
// (or the whole field when none); argv is split on NUL within its 512-byte
// field only — nothing past the field is ever looked at, which is how the
// environment block that follows argv in the task's memory can never leak
// into an event even if the kernel side were wrong.
func ParseExecEvent(raw []byte) (ExecEvent, error) {
	if len(raw) < execEventMin {
		return ExecEvent{}, fmt.Errorf("netbpf: exec event sample too short: %d < %d bytes", len(raw), execEventMin)
	}
	b := binary.NativeEndian
	ev := ExecEvent{
		HostPID:       b.Uint32(raw[execOffHostPID:]),
		PPID:          b.Uint32(raw[execOffPPID:]),
		UID:           b.Uint32(raw[execOffUID:]),
		CgroupID:      b.Uint64(raw[execOffCgroupID:]),
		Comm:          cString(raw[execOffComm : execOffComm+execCommLen]),
		Filename:      cString(raw[execOffFilename : execOffFilename+execFilenameLen]),
		Argv:          splitArgv(raw[execOffArgv : execOffArgv+execArgvLen]),
		ArgvTruncated: raw[execOffArgvTruncated] != 0,
	}
	return ev, nil
}

// cString returns the bytes up to the first NUL, or all of them.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// splitArgv splits a NUL-separated argument block. The block normally ends
// with a NUL; a capped block may end mid-argument, which is kept as the last
// element. Trailing zero padding after the final NUL is ignored — which also
// means a trailing EMPTY argument (`cmd ""`) is indistinguishable from the
// padding and is dropped; harmless for name/argv matching, noted here so
// nobody counts on it.
func splitArgv(b []byte) []string {
	// Trim the zero fill after the last argument's terminator.
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	if end == 0 {
		return nil
	}
	// If the block ended in a NUL (the normal case), end now points past the
	// last argument's final byte; split what remains.
	parts := bytes.Split(b[:end], []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, string(p))
	}
	return out
}

// ExecWatch owns a loaded execwatch collection and its tracepoint link.
// Linux-only at runtime, like Loader.
type ExecWatch struct {
	coll *ebpf.Collection
	link link.Link
}

// ExecObjectPathFor maps the operator's netpolicy object path to the exec
// object's: the sibling execwatch.bpf.o in the same directory, so the one
// CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT value drives both objects. A path
// that already names execwatch.bpf.o is returned as is; a directory gets the
// file appended. An empty path returns "" — it must never resolve to a file
// relative to the daemon's working directory.
func ExecObjectPathFor(objPath string) string {
	objPath = strings.TrimSpace(objPath)
	if objPath == "" {
		return ""
	}
	if strings.HasSuffix(objPath, "/") {
		return objPath + ExecObjectName
	}
	if filepath.Base(objPath) == ExecObjectName {
		return objPath
	}
	return filepath.Join(filepath.Dir(objPath), ExecObjectName)
}

// ExecObjectSource says where ResolveExecWatch reads the execwatch object
// from for an operator value: the object compiled into the binary (embedded
// true), or the file at path (the netpolicy path's sibling, ExecObjectPathFor).
// An empty value selects the embedded object here, unlike Resolve and the
// integrity self-measurement of the netpolicy object, where empty means no
// program; the daemon starts the exec watcher only with a non-empty value, so
// the two never disagree in practice (pinned by tests). ResolveExecWatch and
// the integrity self-measurement (#3120) share this so they agree on which
// bytes are loaded.
func ExecObjectSource(value string) (embedded bool, path string) {
	if strings.TrimSpace(value) == "" || IsEmbeddedValue(value) {
		return true, ""
	}
	return false, ExecObjectPathFor(strings.TrimSpace(value))
}

// ResolveExecWatch loads the execwatch object from the same operator value
// Resolve takes: the embedded keyword selects the object compiled into this
// binary; any other value is a netpolicy object path whose sibling
// execwatch.bpf.o is loaded (ExecObjectPathFor). An empty value means
// embedded too (and fails when nothing is embedded) — never a file relative
// to the daemon's working directory.
func ResolveExecWatch(value string) (*ExecWatch, error) {
	embedded, path := ExecObjectSource(value)
	if !embedded {
		return LoadExecWatch(path)
	}
	obj := EmbeddedExecObject()
	if len(obj) == 0 {
		return nil, fmt.Errorf("netbpf: no embedded execwatch BPF object in this build " +
			"(release binaries bundle it; for a custom build run `make build-bpf` then build with -tags embed_bpf)")
	}
	return LoadExecWatchFromBytes(obj)
}

// LoadExecWatch reads the compiled execwatch object at objPath and loads the
// collection (verifier runs here; nothing is attached yet).
func LoadExecWatch(objPath string) (*ExecWatch, error) {
	if _, err := os.Stat(objPath); err != nil {
		return nil, fmt.Errorf("netbpf: execwatch BPF object %q: %w", objPath, err)
	}
	spec, err := ebpf.LoadCollectionSpec(objPath)
	if err != nil {
		return nil, fmt.Errorf("netbpf: execwatch: load spec: %w", err)
	}
	return newExecWatchFromSpec(spec)
}

// LoadExecWatchFromBytes loads the collection from an in-memory object (the
// embedded one).
func LoadExecWatchFromBytes(obj []byte) (*ExecWatch, error) {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(obj))
	if err != nil {
		return nil, fmt.Errorf("netbpf: execwatch: load spec from embedded object: %w", err)
	}
	return newExecWatchFromSpec(spec)
}

func newExecWatchFromSpec(spec *ebpf.CollectionSpec) (*ExecWatch, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("netbpf: execwatch: RemoveMemlock: %w", err)
	}
	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		// A missing kernel BTF or a CO-RE relocation failure surfaces here —
		// the reason the daemon reports as `exec-telemetry: unavailable (...)`.
		return nil, fmt.Errorf("netbpf: execwatch: load collection: %w", err)
	}
	for _, name := range []string{mapWatchedCgroups, mapExecEvents, mapExecStats} {
		if coll.Maps[name] == nil {
			coll.Close()
			return nil, fmt.Errorf("netbpf: execwatch BPF object missing map %q (rebuild execwatch.bpf.o?)", name)
		}
	}
	if coll.Programs[progExecWatch] == nil {
		coll.Close()
		return nil, fmt.Errorf("netbpf: execwatch BPF object missing program %q", progExecWatch)
	}
	return &ExecWatch{coll: coll}, nil
}

// Attach hooks the program onto the sched/sched_process_exec tracepoint.
// Idempotent. Nothing is emitted until WatchCgroup registers a cgroup.
func (w *ExecWatch) Attach() error {
	if w.link != nil {
		return nil
	}
	lnk, err := link.Tracepoint(execTracepointGroup, execTracepointName, w.coll.Programs[progExecWatch], nil)
	if err != nil {
		return fmt.Errorf("netbpf: execwatch: attach tracepoint %s/%s: %w", execTracepointGroup, execTracepointName, err)
	}
	w.link = lnk
	return nil
}

// WatchCgroup registers a cgroup id (the inode of /sys/fs/cgroup/<path>) so
// execs under it — at any depth — are emitted. index is opaque to the kernel.
// Upsert semantics: re-registering an id is a no-op apart from the index.
func (w *ExecWatch) WatchCgroup(cgroupID uint64, index uint32) error {
	if err := w.coll.Maps[mapWatchedCgroups].Update(&cgroupID, &index, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("netbpf: execwatch: update watched_cgroups[%d]: %w", cgroupID, err)
	}
	return nil
}

// UnwatchCgroup removes a cgroup id. A missing key is not an error (the
// desired state is already reached), like DeleteEgress.
func (w *ExecWatch) UnwatchCgroup(cgroupID uint64) error {
	if err := w.coll.Maps[mapWatchedCgroups].Delete(&cgroupID); err != nil {
		if errors.Is(err, ebpf.ErrKeyNotExist) {
			return nil
		}
		return fmt.Errorf("netbpf: execwatch: delete watched_cgroups[%d]: %w", cgroupID, err)
	}
	return nil
}

