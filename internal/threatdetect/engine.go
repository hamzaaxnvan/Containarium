package threatdetect

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// RuleContext is what a Rule needs to evaluate flows/deny events, without
// talking to Incus or the DB directly — this is what keeps rules pure and
// table-testable, and lets the engine recover a panicking rule without
// touching shared state.
type RuleContext struct {
	// TenantForIP resolves a container IP to its owning tenant id, backed by
	// the network-policy enforcer's ip_tenant cache (refreshed every 10s
	// reconcile — see NetworkPolicyEnforcer.TenantForIP). ok is false for an
	// unrecognized IP; a rule must never guess at an unknown IP's tenant.
	TenantForIP func(ip netip.Addr) (tenantID string, ok bool)
	// Now is injected so time-window rules (deny-burst) are testable with a
	// fake clock instead of wall time.
	Now func() time.Time
	// MiningThresholds returns the operator-tuned mining-behavior thresholds
	// (#2635), read from the daemon config store by the caller. nil means
	// DefaultMiningThresholds.
	MiningThresholds func() MiningThresholds
	// HashExec asks the host to hash the exec'd binary (/proc/<pid>/exe)
	// off the engine's exec path and redeliver ev through OnExec with
	// SHA256 set (#2638). Best effort: the process may be gone, and a full
	// queue drops the request. nil when no hasher is wired.
	HashExec func(ev ExecEvent)
}

// CPUSample is one container's CPU usage over one metrics-collector tick
// (#2635), derived from the collector's cumulative CPUUsageSeconds. IP is
// the container's address, which joins the sample to the flow records whose
// source it is.
type CPUSample struct {
	Container string
	IP        netip.Addr
	// Cores is the mean number of busy cores over the tick (1.0 = one core).
	Cores float64
	// LimitCores is the box's limits.cpu in cores; 0 when unlimited.
	LimitCores float64
	At         time.Time
}

// CPUConsumer is an optional interface a Rule implements to receive CPU
// samples. The engine type-asserts on Register, so rules that do not need
// CPU stay unchanged.
type CPUConsumer interface {
	OnCPU(ctx RuleContext, samples []CPUSample) []RawFinding
}

// ExecEvent is one process exec inside a tenant container, as the host-side
// exec telemetry (#2637) delivers it: the kernel record decoded and
// attributed to its container by the server's ExecWatcher. PID and PPID are
// host-namespace ids. Argv is the argument vector, capped at 512 bytes by
// the kernel program and never containing the environment. SHA256 is
// empty on the first delivery; a rule that needs it asks the watcher to hash
// /proc/<pid>/exe and gets a second OnExec with it filled (#2638).
// StartTime is the watcher's boot-clock stamp at decode, in /proc/<pid>/stat
// field 22 units: an upper bound for the process's start time, 0 when the
// clock could not be read (#2765).
type ExecEvent struct {
	Container string
	TenantID  string
	At        time.Time
	PID       uint32
	StartTime ProcStartTicks
	PPID      uint32
	UID       uint32
	Comm      string
	Filename  string
	// Argv is the real argument vector (netbpf.ExecEvent.Argv, split on
	// NUL), so an argument holding spaces stays one argument: argv markers
	// match argument boundaries, not the joined line (#2768).
	Argv          []string
	ArgvTruncated bool
	SHA256        string
}

// ArgvString joins Argv with single spaces: the display form the finding
// evidence, logs and the CLI carry.
func (e ExecEvent) ArgvString() string { return strings.Join(e.Argv, " ") }

// ExecConsumer is an optional interface a Rule implements to receive exec
// events (#2637), the exec twin of CPUConsumer: type-asserted on Register,
// fanned out by OnExec with the same per-rule panic recovery.
type ExecConsumer interface {
	OnExec(ctx RuleContext, ev ExecEvent) []RawFinding
}

// RawFinding is what a Rule returns when it fires: enough to build a
// Finding, without the store/lifecycle fields (id, state, count, first/last
// seen) that belong to the engine and the store.
type RawFinding struct {
	Rule      pb.ThreatRuleId
	Severity  pb.ThreatSeverity
	TenantID  string
	Container string
