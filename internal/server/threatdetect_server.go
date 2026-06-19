package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ThreatDetectionServer implements ThreatDetectionService. GetSentryStatus
// ships with #1640 (background detection loop); ListBadDestinations/
// AddBadDestination/RemoveBadDestination ship with #1641 (known-bad
// destination rule); ListFindings/ResolveFinding ship with #1643 (findings
// delivery + triage) — see docs/architecture/continuous-threat-detection.md.
type ThreatDetectionServer struct {
	pb.UnimplementedThreatDetectionServiceServer

	// engine is nil whenever sentryEnabled or ebpfAvailable is false — a
	// disabled or unavailable sentry never constructs one.
	engine *threatdetect.Engine

	sentryEnabled bool // CONTAINARIUM_THREAT_SENTRY=1

	// badDestRule is constructed independent of sentryEnabled/ebpfAvailable
	// — an operator can curate the known-bad-destination list before ever
	// flipping CONTAINARIUM_THREAT_SENTRY on. nil only if construction
	// itself failed (malformed embedded baseline list — a build-time bug,
	// not a runtime condition).
	badDestRule *threatdetect.BadDestinationRule

	// store backs ListFindings/ResolveFinding (#1643). Independent of
	// sentryEnabled/ebpfAvailable — an operator can triage findings a
	// now-disabled sentry already recorded. nil (both Postgres and the
	// in-memory fallback failed to construct) reports Unavailable rather
	// than panicking, same as badDestRule.
	store threatdetect.FindingReader

	// policies and responder back the process-policy RPCs (#2636); see
	// threatdetect_policy_server.go. Either may be nil.
	policies  *threatdetect.ProcessPolicyStore
	miningCfg *threatdetect.MiningThresholdsConfig
	responder *ProcessResponder

	// minerSigRule backs the miner-signature RPCs (#2638); like badDestRule
	// it exists whether or not the sentry is armed. See
	// threatdetect_minersig_server.go.
	minerSigRule *threatdetect.MinerSignatureRule

	mu                sync.Mutex // guards the fields below — SetUnavailable / SetCPUSamplerStatus run after Start()
	ebpfAvailable     bool       // eBPF object loaded (networkPolicyEnforcer != nil) AND still running
	unavailableReason string
	cpuSamplerStatus  string              // GetSentryStatus.cpu_sampler (#2645); "" until wired
	execTelemetry     execTelemetrySource // exec telemetry (#2637); nil until wired
	execHash          execHashSource      // miner-signature exec hasher (#2768); nil until wired

	// responderDegradedReason is why the process responder is ALERT-only
	// ("" when it can apply THROTTLE/FREEZE). Reported as DEGRADED even when
	// the finding store itself is persisted (#2719).
	responderDegradedReason string
}

// execTelemetrySource is what the server needs from the ExecWatcher (#2637):
// its status line, the kernel's lost count, and a subscription for
// StreamExecEvents. An interface so the handler is testable with a fake.
type execTelemetrySource interface {
	Status() string
	Lost() int64
	Subscribe(container string, since time.Duration, buffer int) (replay []ExecEventRecord, ch <-chan ExecEventRecord, cancel func())
}

// execHashSource is what the server reads from the exec hasher (#2768): the
// count of hash requests dropped on a full queue.
type execHashSource interface {
	Dropped() int64
}

// SetExecHashSource wires the exec hasher: GetSentryStatus reports its
// Dropped as exec_hashes_dropped.
func (s *ThreatDetectionServer) SetExecHashSource(src execHashSource) {
	s.mu.Lock()
	s.execHash = src
	s.mu.Unlock()
}

// NewThreatDetectionServer builds the server. engine must be non-nil iff
// sentryEnabled && ebpfAvailable — the constructor doesn't re-derive that
// invariant, the daemon wiring (dual_server.go) is the one place that
// decides whether to construct an Engine at all. unavailableReason is
// surfaced verbatim on GetSentryStatus when !ebpfAvailable; it's ignored
// otherwise.
func NewThreatDetectionServer(engine *threatdetect.Engine, sentryEnabled, ebpfAvailable bool, unavailableReason string) *ThreatDetectionServer {
	return &ThreatDetectionServer{
		engine:            engine,
		sentryEnabled:     sentryEnabled,
		ebpfAvailable:     ebpfAvailable,
		unavailableReason: unavailableReason,
	}
}

// SetCPUSamplerStatus records which source feeds the mining-behavior
// rule's CPU samples (#2645) — "metrics-collector", "standalone (30s)" or
// "unavailable (<reason>)" — surfaced as GetSentryStatus.cpu_sampler.
func (s *ThreatDetectionServer) SetCPUSamplerStatus(status string) {
	s.mu.Lock()
	s.cpuSamplerStatus = status
	s.mu.Unlock()
}

// SetExecTelemetry wires the exec watcher (#2637): GetSentryStatus reports
// its Status as exec_telemetry and its Lost as exec_events_lost, and
// StreamExecEvents subscribes to it. nil (never constructed) reads as
// "unavailable (not started)".
func (s *ThreatDetectionServer) SetExecTelemetry(src execTelemetrySource) {
	s.mu.Lock()
	s.execTelemetry = src
	s.mu.Unlock()
}

// SetBadDestinationRule wires the known-bad-destination rule (#1641) into
// the server's List/Add/RemoveBadDestination handlers. Separate from
// NewThreatDetectionServer's constructor args because the rule is
// constructed independent of whether the sentry itself is enabled — an
// operator can curate the list before ever setting
// CONTAINARIUM_THREAT_SENTRY. nil is valid (construction failure); the
// three handlers report that explicitly rather than panicking.
func (s *ThreatDetectionServer) SetBadDestinationRule(r *threatdetect.BadDestinationRule) {
	s.badDestRule = r
}

// SetFindingStore wires the finding store (#1643) into ListFindings and
// ResolveFinding — *threatdetect.FindingStore (Postgres) or
// *threatdetect.MemFindingStore (degraded) both satisfy FindingReader. nil
// is valid (neither store could be constructed); the two handlers report
// that explicitly rather than panicking.
func (s *ThreatDetectionServer) SetFindingStore(store threatdetect.FindingReader) {
	s.store = store
