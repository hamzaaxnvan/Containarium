// Package threatdetect holds the background security-detection engine: the
// rule registry, the finding store, and delivery. See
// docs/architecture/continuous-threat-detection.md.
package threatdetect

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ErrFindingNotFound reports that Get/Resolve was called with an id that
// doesn't exist. Both FindingStore and MemFindingStore return this exact
// sentinel (wrapped, checked with errors.Is) rather than leaking a
// backend-specific not-found signal (pgx.ErrNoRows for FindingStore) past
// the store boundary — callers like ThreatDetectionServer.ResolveFinding
// map it to codes.NotFound without knowing which backend is in play.
var ErrFindingNotFound = errors.New("finding not found")

// ErrFindingNotOpen reports that Resolve was called on a finding that
// exists but isn't currently open (already resolved). Distinct from
// ErrFindingNotFound so callers can map the two to different response
// codes (NotFound vs. FailedPrecondition).
var ErrFindingNotOpen = errors.New("finding is not open")

// timestampProto converts a zero-safe time.Time to a *timestamppb.Timestamp,
// returning nil for a zero time rather than the protobuf epoch.
func timestampProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// EvidenceCap is the maximum number of flow/deny records kept per finding.
// Evidence exists to show an operator a representative sample, not a full
// trace — an unbounded finding row would grow without limit under a
// sustained attack.
const EvidenceCap = 10

// FlowEvidence is one triggering flow's 5-tuple and volume.
type FlowEvidence struct {
	SrcIP    string `json:"src_ip"`
	DstIP    string `json:"dst_ip"`
	SrcPort  uint32 `json:"src_port"`
	DstPort  uint32 `json:"dst_port"`
	Protocol string `json:"protocol"`
	Bytes    int64  `json:"bytes"`
	Packets  int64  `json:"packets"`
	// FirstSeen/LastSeen and Matched are set by the mining-behavior rule
	// (#2635) only; see pb.FlowEvidence.
	FirstSeen time.Time `json:"first_seen,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	Matched   string    `json:"matched,omitempty"`
}

// DenyEvidence is an aggregated count of policy-deny events matching one
// destination/reason pair.
type DenyEvidence struct {
	DstIP    string `json:"dst_ip"`
	DstPort  uint32 `json:"dst_port"`
	Protocol string `json:"protocol"`
	Reason   string `json:"reason"`
	Count    int64  `json:"count"`
}

// ConfigEvidence is one configuration directive whose effective value
// violated policy (#2424): which file, which directive, what value, and
// whether the platform re-asserted its managed configuration in the same
// pass.
type ConfigEvidence struct {
	Path       string `json:"path"`
	Directive  string `json:"directive"`
	Value      string `json:"value"`
	Remediated bool   `json:"remediated"`
	Note       string `json:"note,omitempty"`
}

// ListenerEvidence is one listening socket held by an SSH server that is
// not the box's distro sshd service (#2439).
type ListenerEvidence struct {
	Port        uint32 `json:"port"`
	BindAddress string `json:"bind_address"`
	PID         uint32 `json:"pid"`
	Binary      string `json:"binary"`
	Note        string `json:"note,omitempty"`
}

// CPUEvidence summarises a container's CPU usage over a detection window
// (#2635), in cores.
type CPUEvidence struct {
	MeanCores     float64 `json:"mean_cores"`
	PeakCores     float64 `json:"peak_cores"`
	WindowSeconds int64   `json:"window_seconds"`
	Samples       int32   `json:"samples"`
}

// ProcStartTicks is a point on the boot clock in /proc/<pid>/stat field 22
// units: clock ticks (USER_HZ) since boot (CLOCK_BOOTTIME). A process's
// field 22 (its fork time) is constant for its life, and the exec watcher
// stamps each exec on the same clock; the kill refuses a pid whose start
// time is later than the matched exec's stamp, which is the identity check
// against pid reuse (#2765). 0 means unknown. Never converted to a
// wall-clock time.
type ProcStartTicks uint64

// ExecEvidence is one process exec attached to a finding (#2637, used by the
// miner-signature rule #2638): the host-side exec telemetry record,
// attributed to the container. PID/PPID are host-namespace ids.
type ExecEvidence struct {
	PID           uint32 `json:"pid"`
	PPID          uint32 `json:"ppid"`
	UID           uint32 `json:"uid"`
	Comm          string `json:"comm"`
	Filename      string `json:"filename"`
	Argv          string `json:"argv"`
	ArgvTruncated bool   `json:"argv_truncated,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	Matched       string `json:"matched,omitempty"`

	// StartTime is the boot-clock stamp the exec watcher put on the exec when
	// it decoded it: an upper bound for the process's start time (#2765). The
	// kill refuses a pid that started after it. Not on the wire message: it
	// is an internal identity check.
	StartTime ProcStartTicks `json:"start_time,omitempty"`
}

// ToProto renders the evidence as the wire message.
func (e ExecEvidence) ToProto() *pb.ExecEvidence {
	return &pb.ExecEvidence{
		Pid:           e.PID,
		Ppid:          e.PPID,
		Uid:           e.UID,
		Comm:          e.Comm,
		Filename:      e.Filename,
		Argv:          e.Argv,
		ArgvTruncated: e.ArgvTruncated,
		Sha256:        e.SHA256,
		Matched:       e.Matched,
	}
}

// Evidence bundles the flow, deny and configuration records that triggered
// a finding. It is the named struct written into the
// security_findings.evidence JSONB column — a storage encoding, never an
// ad-hoc map.
type Evidence struct {
	Flows     []FlowEvidence     `json:"flows,omitempty"`
	Denies    []DenyEvidence     `json:"denies,omitempty"`
	Configs   []ConfigEvidence   `json:"configs,omitempty"`
	Listeners []ListenerEvidence `json:"listeners,omitempty"`
	CPU       []CPUEvidence      `json:"cpu,omitempty"`
	Execs     []ExecEvidence     `json:"execs,omitempty"`
}

// Capped returns a copy of e with each evidence kind truncated to the most
// recent EvidenceCap entries.
func (e Evidence) Capped() Evidence {
	out := Evidence{Flows: e.Flows, Denies: e.Denies, Configs: e.Configs, Listeners: e.Listeners, CPU: e.CPU, Execs: e.Execs}
	if len(out.Flows) > EvidenceCap {
		out.Flows = out.Flows[len(out.Flows)-EvidenceCap:]
	}
	if len(out.Denies) > EvidenceCap {
		out.Denies = out.Denies[len(out.Denies)-EvidenceCap:]
	}
	if len(out.Configs) > EvidenceCap {
		out.Configs = out.Configs[len(out.Configs)-EvidenceCap:]
	}
	if len(out.Listeners) > EvidenceCap {
		out.Listeners = out.Listeners[len(out.Listeners)-EvidenceCap:]
	}
	if len(out.CPU) > EvidenceCap {
		out.CPU = out.CPU[len(out.CPU)-EvidenceCap:]
	}
	if len(out.Execs) > EvidenceCap {
		out.Execs = out.Execs[len(out.Execs)-EvidenceCap:]
	}
	return out
}

// merged returns e followed by more, for the upsert paths that append a
// re-fire's evidence onto an open finding's.
// maxSeverity returns the more severe of a and b. A deduped repeat can only
// raise an open finding's severity, never lower it: a box that was MEDIUM
// (tampering undone) and is now HIGH (still permissive) must escalate, while
// a later quieter pass must not hide that it was once worse.
func maxSeverity(a, b pb.ThreatSeverity) pb.ThreatSeverity {
	if b > a {
		return b
	}
	return a
}

func (e Evidence) merged(more Evidence) Evidence {
	return Evidence{
		Flows:     append(append([]FlowEvidence(nil), e.Flows...), more.Flows...),
		Denies:    append(append([]DenyEvidence(nil), e.Denies...), more.Denies...),
		Configs:   mergeConfigEvidence(e.Configs, more.Configs),
		Listeners: append(append([]ListenerEvidence(nil), e.Listeners...), more.Listeners...),
		CPU:       append(append([]CPUEvidence(nil), e.CPU...), more.CPU...),
		Execs:     append(append([]ExecEvidence(nil), e.Execs...), more.Execs...),
	}
}

// mergeConfigEvidence appends the entries of more that old does not already
// hold. Config evidence is a state snapshot, not an event: a box that stays
// permissive re-reports the identical entry every reconcile pass, and
// appending each repeat would fill the capped list with copies and push out
// anything that differs. A repeat still bumps the finding's count and
// last-seen; the evidence grows only when something changed.
func mergeConfigEvidence(old, more []ConfigEvidence) []ConfigEvidence {
	out := append([]ConfigEvidence(nil), old...)
	for _, c := range more {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// FindingResponse records what the process responder (#2636) did about a
// finding: the action, its state, and when it was applied, expires and was
