package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/alert"
	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/capabilities"
	"github.com/footprintai/containarium/internal/capacity"
	appconfig "github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/controller"
	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/guacamole"
	"github.com/footprintai/containarium/internal/integrity"
	"github.com/footprintai/containarium/internal/metrics/cloudexport"
	"github.com/footprintai/containarium/internal/metrics/platformstats"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/internal/proofpack"
	"github.com/footprintai/containarium/internal/releasecheck"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/internal/sshauthz"
	"github.com/footprintai/containarium/internal/toolaccess"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/tracker/submit"
	"github.com/footprintai/containarium/pkg/core/backup"
	"github.com/footprintai/containarium/pkg/core/box"
	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	"github.com/footprintai/containarium/pkg/core/ostype"
	"github.com/footprintai/containarium/pkg/core/stacks"
	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/footprintai/containarium/pkg/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PendingCreation tracks an async container creation
type PendingCreation struct {
	Username     string
	StartedAt    time.Time
	Error        error
	Done         bool
	Provisioning bool // container is running but installing stack/packages
	// Cancelled records that a DeleteContainer arrived while this creation
	// was still in flight (#1035). Provisioning runs for minutes; a delete
	// pulls the instance out from under the goroutine, which then fails with
	// "Instance not found" — indistinguishable, in the journal, from a
	// genuine create failure. Flagging the cancellation lets the goroutine
	// log what actually happened, and keeps the doomed creation out of the
	// CREATING/ERROR state that Get/List would otherwise report.
	Cancelled bool
}

// active reports whether this creation is still legitimately in flight —
// neither finished nor cancelled by a delete. Read/List overlays must use
// this rather than !Done, or a cancelled create keeps reporting CREATING
// for the rest of its (now pointless) provisioning run.
func (p *PendingCreation) active() bool { return p != nil && !p.Done && !p.Cancelled }

// ContainerServer implements the gRPC ContainerService
type ContainerServer struct {
	nativeTenantReady func() bool
	pb.UnimplementedContainerServiceServer
	// systemInfoFn overrides the local GetSystemInfo probe in ListBackends.
	// Nil in production; a test seam for a wedged local incusd (#2318).
	systemInfoFn func(context.Context, *pb.GetSystemInfoRequest) (*pb.GetSystemInfoResponse, error)
	// TrackerService is its own proto service (tracker.proto), not part
	// of ContainerService — embedding its Unimplemented server here lets
	// ContainerServer satisfy pb.TrackerServiceServer without a
	// second struct, since TrackerService's handlers already need
	// secretsStore for the broker-only credential cross-check.
	pb.UnimplementedTrackerServiceServer
	manager *container.Manager
	// boxBackend is the runtime-neutral seam over the box substrate. Today it
	// wraps the same *container.Manager (LXC/incus); a K8s backend slots in
	// here behind a build tag. Box-lifecycle operations (Create/Get/…) route
	// through it; the LXC-specific surface (Exec, config keys, security scans,
	// app hosting) still calls manager directly during the transition.
	boxBackend box.BoxBackend
	// boxWriter, when non-nil (k8s runtime + operator enabled), makes the
	// imperative create path write a Box CR instead of calling boxBackend
	// directly — the operator then reconciles it, so both the imperative and
	// declarative paths converge on one builder (#995, slice 4).
	boxWriter *controller.BoxWriter
	// keyProvider supplies per-tenant ZFS encryption keys. Nil on a
	// daemon with no key custody configured — which is every OSS daemon
	// today — in which case an encrypted create is refused rather than
	// silently producing plaintext (#1198). The lifecycle hooks that
	// consume it land in #1199/#1201.
	keyProvider zfskey.KeyProvider
	// encryption carries the per-tenant dataset hooks (#1199/#1201). A
	// nil value is a valid no-op receiver, so the lifecycle paths do not
	// branch on whether encryption is configured.
	encryption *encryptionHooks
	// snapshots is the container-snapshot surface (#1160). nil when the
	// storage backend is not ZFS, which the handlers report rather than
	// panic on.
	snapshots           *snapshotOps
	collaboratorManager *container.CollaboratorManager
	// sshKeys is the ssh_keys registry the key write paths record into
	// (#2517, ssh_key_registry.go). Nil when the daemon has no Postgres.
	sshKeys sshauthz.KeyRegistry
	// collabAccess and hostSessionKill are test seams for
	// TerminateUserSessions (terminate_user_sessions.go); nil means the
	// collaborator store and container.TerminateJumpAccountSessions.
	collabAccess    collaboratorAccess
	hostSessionKill func(account string) bool
	// sshKeyNow and authorizedKeys are test seams for the key write paths;
	// zero values mean time.Now and the host's real authorized_keys files.
	sshKeyNow      func() time.Time
	authorizedKeys authorizedKeysOps
	// Box code-run reader (#2123, box_run_log.go). All four are test seams:
	// boxRunExec replaces the container manager's exec, boxRunCollaborator
	// the collaborator lookup, boxRunLogDir agent-box's log dir, and
	// boxRunPoll the follow poll interval. Zero values mean production.
	boxRunExec         boxScriptFunc
	boxRunCollaborator collaboratorCheckFunc
	boxRunLogDir       string
	boxRunPoll         time.Duration
	// boxRunStartExec is StartBoxRun's own exec seam (#2193), separate from
	// boxRunExec: production needs to run AS the box's own Linux user (so the
	// spawned engine reads that user's credentials and the files it creates
	// are owned the same way an SSH-driven `code run` leaves them), which
	// means wrapping the script in `su - <username> -c`. That wrapping is a
	// production-exec-layer detail, not something boxRunStartScript's own
	// text should encode — a test target has no real multi-user box to su
	// into, so it sets this directly to a plain `sh -c` runner, exactly like
	// boxRunExec's existing tests do for List/Tail.
	boxRunStartExec boxRunStartFunc

	// approvalSecret is what box approval keys are derived from (#2184,
	// box_run_inputs.go): the daemon's JWT secret, set by DualServer. Nil
	// leaves the approval RPCs Unavailable. runInputConfirmWait is a test
	// seam for how long AnswerRunInput waits for the box's response; zero
	// means the default.
	approvalSecret      []byte
	runInputConfirmWait time.Duration
	// runInputJournalCap is a test seam for maxRunInputJournal; zero means
	// the default.
	runInputJournalCap int64

	// Per-run checkout sweep (#3296, box_run_checkout_sweep.go).
	// runCheckoutMaxAge is --run-checkout-max-age (0 = off), meaningful only
	// once SetRunCheckoutMaxAge set runCheckoutMaxAgeSet; unset means the
	// default. The rest are test seams, nil in production: runCheckoutNow the
	// clock, runCheckoutHomes the box users' home lookup, runCheckoutBoxes the
	// box list (and per-box status), runCheckoutLog the logger.
	runCheckoutMaxAge    time.Duration
	runCheckoutMaxAgeSet bool
	runCheckoutNow       func() time.Time
	runCheckoutHomes     func(box string, users []string) (map[string]string, error)
	runCheckoutBoxes     func(ctx context.Context) ([]box.BoxStatus, error)
	runCheckoutLog       func(format string, args ...any)
	runCheckoutLocks     sync.Map // key: box name -> *sync.Mutex

	// boxRunToolAccess overrides how StartBoxRun reads the tool-access level
	// recorded for a box (#2944): "" = none recorded, so the run keeps the
	// owner's identity. Tests set it; production reads the Incus config.
	boxRunToolAccess      func(ctx context.Context, username string) (string, error)
	boxRunEnsureToolchain func(username string) error // test seam; production uses manager
	// boxRunLocks serializes StartBoxRun's check-then-write span (collision
	// check through record write) per (box, name) (#2260 review: concurrent
	// calls for the same not-yet-existing name could both pass the collision
	// check, then race on the log truncate and the record write, corrupting
	// the log and orphaning a PID with no record naming it). sync.Map's zero
	// value is ready to use — no constructor needed, so test literals that
	// build a bare &ContainerServer{} still get correct serialization.
	boxRunLocks      sync.Map // key: box+":"+name -> *sync.Mutex
	emitter          *events.Emitter
	pendingCreations map[string]*PendingCreation
	pendingMu        sync.RWMutex
	// Monitoring URLs (set by DualServer after setup)
	victoriaMetricsURL string
	grafanaURL         string
	// Alerting (set by DualServer after setup)
	alertStore         *alert.Store
	alertManager       *alert.Manager
	alertDeliveryStore *alert.DeliveryStore
	alertWebhookURL    string
	alertWebhookSecret string
	hostRelayURL       string                          // e.g. "http://10.100.0.1:8080/internal/alert-relay"
	alertRelayConfigFn func(webhookURL, secret string) // callback to update gateway relay config
	coreServices       *CoreServices
	daemonConfigStore  daemonConfigKV
	peerPool           *PeerPool
	// backupMgr is the backup core's read side, wired in for the metrics
	// export collector's backup-health series (set by DualServer after
	// setup, #2294) — nil-safe like peerPool/alertStore above. Not used
	// for anything but SetBackupManager's seam into serverPlatformSources.
	backupMgr *backup.Manager
	// Cloud-native metrics export (#1069). metricsExportMu guards the
	// in-memory config so SetMetricsExport/GetMetricsExport round-trip
	// without a daemon restart; daemonConfigStore (when present) makes
	// the toggle survive one. metricsExportSinks maps a provider enum
	// value to its Sink so the enable-time credential probe is
	// injectable in tests without touching real GCP ADC. Both are wired
	// once at startup (DualServer calls SetMetricsExportSinks with the
	// real gcpSink) and otherwise left nil, which GetMetricsExport
	// treats as "never configured" and SetMetricsExport treats as
	// Unimplemented for that provider.
	metricsExportMu           sync.RWMutex
	metricsExportConfig       cloudexport.Config
	metricsExportConfigLoaded bool
	metricsExportSinks        map[pb.CloudMetricsProvider]cloudexport.Sink
	// metricsExportCollector is the running host-series export pipeline
	// (#1070), non-nil only while export is enabled. SetMetricsExport
	// starts it on enable and stops it on disable; GetMetricsExport reads
	// its health. Guarded by metricsExportMu, same as the config/sinks it
	// lives beside — one lock for the whole export subsystem.
	metricsExportCollector *cloudexport.CloudExportCollector
	// metricsExportBuilder is a test seam over buildMetricsExportCollector:
	// the real builder dials Incus and needs a live Manager, so server-
	// level tests inject a fake that returns a collector over fake
	// sources/exporter. nil in production (the real builder runs).
	metricsExportBuilder func(ctx context.Context, cfg cloudexport.Config, sink cloudexport.Sink) (*cloudexport.CloudExportCollector, error)
	// platformStats accumulates platform-domain facts (#1082 API
	// request/error counts today; #1083/#1084 add more) for the platform
	// metric group to observe at export tick. Recorded on the gRPC
	// unary-interceptor hot path (DualServer wires
	// platformstats.UnaryInterceptor over this same instance), read
	// through cloudexport.PlatformSources by buildMetricsExportCollector.
	// Lock-free (atomic counters inside), so no mutex needed here.
	platformStats *platformstats.Stats
	// localHealthCheckFn overrides localBackendHealthy's real Incus liveness
	// probe (#920) — set by tests to simulate a wedged/unresponsive local
	// backend without a live Incus daemon. nil in production; the real probe
	// runs.
	localHealthCheckFn func() bool
	// localHealthOnce / localHealthState: the debounced, single-flight local
	// liveness verdict behind localBackendHealthy (#2317). Built lazily on first
	// use so the env override is read once.
	localHealthOnce  sync.Once
	localHealthState *localHealth
	// CPU overcommit admission (#1029 direction 2). cpuOvercommitFactor is the
	// ceiling multiple of physical cores a host may commit; <= 0 disables the
	// gate (the default). cpuOvercommitEnforce=false makes an enabled gate
	// advisory (log-only). hostCoresFn is a test seam for the host's physical
	// core count (mirrors localHealthCheckFn); nil reads Incus in production.
	// See cpu_admission.go.
	cpuOvercommitFactor  float64
	cpuOvercommitEnforce bool
	hostCoresFn          func() (float64, error)
	// cpuReservations closes the #1588 check-then-act race: admitCPUCapacity
	// reads a snapshot of committed cores with no lock held across the
	// caller's subsequent mutation, so two concurrent admits against the
	// same host can each pass against a stale snapshot and jointly exceed
	// the ceiling. Every admitted (or advisory-logged) request reserves its
	// cores here for the duration of the caller's mutation; a later admit
	// checks committed cores plus every other tenant's live reservation, not
	// just the real (possibly stale) committed total. The caller releases
	// its reservation once the mutation concludes, success or failure — see
	// admitCPUCapacity's returned release func. cpuReservationTTL is a
	// safety net, not the primary mechanism: a caller whose completion this
	// package can't observe (the k8s Box-CR reconciliation path) never
	// calls release, and would otherwise hold a reservation forever.
	cpuReservationsMu sync.Mutex
	cpuReservations   map[string]cpuReservation
	// nowFn overrides cpuNow's real time.Now() (mirrors hostCoresFn /
	// localHealthCheckFn above) so a test can exercise reservation TTL
	// expiry without an actual 10-minute wait. nil in production.
	nowFn             func() time.Time
	cpuReservationSeq uint64
	// capacityStore holds this backend's spare-capacity advertise/withdraw
	// state + local policy (#680). Lazily initialized so an unwired server
	// (tests) still answers GetCapacityHeadroom with "not advertised".
	capacityStore     *capacity.Store
	capacityStoreOnce sync.Once
	// capabilityStore holds this backend's last-recorded hardware capability
	// profile (#681). Lazily initialized like capacityStore so an unwired
	// server (tests) answers GetCapabilityProfile with "not profiled yet".
	capabilityStore     *capabilities.Store
	capabilityStoreOnce sync.Once
	// drainer performs the bounded graceful reclaim of guest workloads when a
	// backend withdraws headroom (#682). Lazily initialized; a single in-flight
	// drain at a time guards against repeated advertise/withdraw cycles wedging
	// the host.
	drainer     *capacity.Drainer
	drainerOnce sync.Once
	// profileMu serializes ProfileBackend: each profile spins a throwaway
	// GPU-probe LXC + a benchmark on the host, so concurrent/repeated calls
	// must coalesce rather than stack a probe storm that can wedge the runtime.
	profileMu sync.Mutex
	// Hardware operations default to the bounded benchmark and the manager's
	// passthrough probe; injectable so failures can be exercised without Incus.
	capabilityBenchmark func() (container.BenchmarkResult, error)
	capabilityGPUProbe  func() container.GPUValidationResult
	capabilityResources func() (*incus.SystemResources, error)
	// region is the region this backend serves, wired from --region (falling
	// back to the pool name). Recorded into the capability profile. Empty when
	// unset.
	region string
	// integrityConfigState is the integrity-relevant policy/config posture this
	// backend folds into its signed self-measurement (#683): base domain,
	// network-policy enforcement posture, and any other config the control plane
	// verifies hasn't been tampered with. Wired once via SetIntegrityConfig;
	// nil/empty on an unwired server still yields a (config-empty) measurement.
	integrityConfigState map[string]string
	// integrityExecWatch reports, per measurement, whether the exec watcher
	// actually loaded and attached its object (#3120). Nil means no watcher.
	integrityExecWatch func() bool
	// reportedClass is the self-reported hardware class the operator assigned
	// this backend (defaults to the pool name). Reconciled against the measured
	// class in the capability profile. Empty when unset.
	reportedClass string
	// startTime is when this daemon process started; ListBackends reports
	// the local backend's uptime from it. Set by DualServer wiring
	// (SetStartTime); zero on a server that was never wired, in which case
	// uptime is reported as 0.
	startTime time.Time
	// Route / Caddy cleanup deps (set by DualServer wiring, may be nil if
	// the daemon was started without --app-hosting). Used by DeleteContainer
	// to cascade-remove the routes / TLS-automation subjects a container
	// owned, so deleting an LXC actually deletes the public hostname too.
	routeStore   routeLister
	proxyManager *app.ProxyManager
	// passthroughStore, when set, lets DeleteContainer also cascade-remove
	// any TCP/UDP passthrough routes the container owned (#1462) — the
	// passthrough analog of routeStore above. nil on a daemon with no
	// Postgres-backed passthrough store configured, in which case this
	// cascade step is skipped (matching routeStore's own nil handling).
	passthroughStore network.PassthroughStore

	// moveRunner shells out to `incus snapshot/copy/stop/start` for the
	// MoveContainer migration flow. Nil on daemons that don't support
	// migration (MoveContainer returns "not configured" then).
	moveRunner incus.MigrationRunner

	// secretsStore is the tenant-secrets backend. Nil on daemons
	// that don't have Postgres wired up (--standalone); the
	// SecretsService RPCs return Unavailable in that case.
	// CreateContainer / StartContainer call LoadAllForUser to
	// stamp environment.<NAME>=<value> at LXC start time.
	secretsStore *secrets.Store

	// trackerStore holds tracker connections (#1921). Nil on daemons that
	// don't have Postgres wired up; the TrackerService RPCs return
	// Unavailable in that case, same convention as secretsStore.
	trackerStore *tracker.Store

	// trackerDescribers overrides provider -> ReaderProvider resolution
	// (tests only — see SetTrackerDescribers). Nil in production;
	// trackerProviderFor constructs the real adapters. Named for its
	// #1921 origin (DescribeCredential only); widened to the full
	// ReaderProvider in #1922 rather than renamed, since every existing
	// caller already only needs DescribeCredential and the field itself
	// is package-private.
	trackerDescribers map[pb.TrackerProvider]tracker.ReaderProvider

	// runRegistry resolves a run id to the skill/model it belongs to,
	// and answers "is this run still live" — the RunResolver seam
	// #1922's tracker write verbs need (identity stamp, ClaimTrackerIssue).
	// Wired from dual_server.go with the SAME *runlease.Registry
	// instance AgentSkillServer populates; nil on daemons where that
	// wiring hasn't happened (tests construct ContainerServer directly).
	runRegistry *runlease.Registry

	// claimLocks serializes ClaimTrackerIssue calls per (username,
	// connection, issue) within this daemon process — see
	// tracker.ClaimLocks' own doc comment for what the lock does and
	// doesn't cover. Constructed once in dual_server.go; tests that
	// don't exercise ClaimTrackerIssue can leave it nil, in which case
	// the RPC returns Unavailable rather than racing unlocked.
	claimLocks *tracker.ClaimLocks

	// trackerRunStarter is how DispatchTrackerIssues (#2022) starts a
	// run — the RunAgentSkill path, wired in dual_server.go. Nil keeps
	// the RPC returning Unavailable.
	trackerRunStarter tracker.RunStarter

	// submitBoxRunner overrides the box-exec/file-read backend
	// SubmitTrackerChange uses to extract a bundle (#1923). Nil in
	// production, where the real handler falls back to s.manager —
	// *container.Manager already satisfies submit.BoxRunner (same
	// ExecWithOutput/ReadFile shape FetchGitSource uses). Tests
	// substitute a fake here so a unit test never needs a real box.
	submitBoxRunner submit.BoxRunner

	// submitPusher overrides the git-push backend SubmitTrackerChange
	// uses (#1923). Nil in production, where the real handler falls
	// back to submit.NewGitPusher(). Tests substitute a fake so a unit
	// test never shells out to real git or touches the network.
	submitPusher submit.GitPusher
	// proofPackExecer overrides the backend SubmitTrackerChange generates
	// the run's Proof Pack through (#2798); nil means s.manager. Tests only.
	proofPackExecer proofpack.Execer

	// submitFilePusher overrides SubmitTrackerChangeFiles' commit-and-push
	// backend (#3005). Nil in production (submit.NewGitPusher()).
	submitFilePusher submit.FilePusher

	// KMS status snapshot for the KmsService GetKMSStatus RPC.
	// Set once at startup in dual_server.go alongside the secrets
	// store. Read-only after wiring; reflects CONTAINARIUM_KMS_BACKEND
	// + CONTAINARIUM_REQUIRE_ENVELOPE as resolved at boot.
	kmsBackend      string
	kmsDescription  string
	kmsConfigured   bool
	requireEnvelope bool

	// wakeRouter applies the Caddy route swap when a container is
	// auto-slept (SwapToWake) and woken back up (SwapToDirect).
	// Nil on daemons without app hosting or with auto-sleep disabled;
	// the StopForAutoSleep / StartContainer hooks are nil-safe.
	wakeRouter WakeRouter

	// otelCollectorEndpoint is the OTLP/HTTP URL of this daemon's
	// core OTel collector LXC (e.g. "http://10.0.3.142:4318").
	// Stamped into containers created with monitoring=true so the
	// SDK inside ships telemetry without app-side config. Empty
	// means the daemon was started without OTel app-monitoring
	// support; create_container with monitoring=true will log a
	// warning and skip the env-var injection.
	otelCollectorEndpoint string
	// Guacamole integration for Windows VM RDP access
	guacamoleClient *guacamole.Client
	guacamoleUser   string // Guacamole admin username
	guacamolePass   string // Guacamole admin password

	// sshHost is the public host clients dial to SSH into containers this
	// daemon fronts — the sentinel's SSH endpoint (e.g. "region-a.example.com"),
	// set by DualServer wiring from --ssh-host. Stamped onto Container.ssh_host
	// in the read path (alongside Pool) so a client builds its connect target
	// `username@ssh_host` without inferring the host from the IP / config.
	// Empty (direct mode / no sentinel) leaves ssh_host empty and clients fall
	// back to network.ip_address.
	sshHost string

	// managedBackupKey is true when the backup service built a managed-key
	// wrapper (CONTAINARIUM_BACKUP_KMS_KEY_NAME); GetSystemInfo reports it so
	// `backup status` knows unregistered tenants are still encrypted (#3213).
	managedBackupKey bool

	// auditStore records admin-initiated operations (TriggerUpgrade, etc.).
	// Nil on daemons without a Postgres pool; nil is safe (ops are logged but
	// not persisted). #354.
	auditStore *audit.Store

	// autoUpdater drives on-demand daemon upgrades (TriggerUpgrade). Nil on
	// daemons started without an auto-update source (e.g. no sentinel), in
	// which case TriggerUpgrade for the local backend returns Unavailable.
	// upgradeJobs tracks in-flight/terminal upgrade jobs by upgrade_id, and
	// upgradeBusy guards against concurrent upgrades per backend. A successful
	// LOCAL upgrade restarts the daemon, so its job state does not survive —
	// callers confirm via the backend version in ListBackends. #354 Phase B.
	autoUpdater *AutoUpdater
	upgradeMu   sync.Mutex
	upgradeJobs map[string]*upgradeJob
	upgradeBusy map[string]bool

	// binaryPath is this daemon's own executable path, used by the
	// GitHub-direct TriggerUpgrade path (req.GithubTag, #1028) to swap its
	// binary in place. Wired unconditionally by DualServer — independent of
	// autoUpdater, which stays nil until a sentinel URL is configured — so a
	// GitHub-direct upgrade works even on a daemon with no sentinel. Empty
	// (e.g. in unit tests that never call SetBinaryPath) makes the
	// github_tag path return Unavailable, same as an unconfigured autoUpdater
	// does for the sentinel path.
	binaryPath string

	// storagePools returns the pool capacity monitor's last snapshot (#2823).
	// Nil until SetStoragePoolSource; GetSystemInfo then reports no pools.
	storagePools func() poolcapacity.Snapshot
	// headroom refuses creates and snapshots on a pool past its floor
	// (#3128). Nil admits everything.
	headroom *poolcapacity.Guard
}

// upgradeJob is the in-memory record of a daemon upgrade triggered via
// TriggerUpgrade, polled by GetUpgradeStatus. #354.
type upgradeJob struct {
	id             string
	backendID      string
	status         string // in_progress | completed | failed | noop
	currentVersion string
	targetVersion  string // best-effort, "" when unknown (#2171)
	errMsg         string
	completedAt    string
}

// NewContainerServer creates a new container server. runtime selects the box
// backend: "lxc" (default, empty) or "k8s". See RuntimeLXC / RuntimeK8s.
func NewContainerServer(runtime string) (*ContainerServer, error) {
	mgr, err := newManager(runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to create container manager: %w", err)
	}
	bb, err := newBoxBackend(runtime, mgr)
	if err != nil {
		return nil, fmt.Errorf("failed to select box backend: %w", err)
	}
	// Start the declarative Box controller alongside the imperative API when
	// enabled (k8s runtime + CONTAINARIUM_K8S_OPERATOR). No-op otherwise.
	maybeStartBoxOperator(runtime, bb)
	return &ContainerServer{
		manager:          mgr,
		boxBackend:       bb,
		boxWriter:        newBoxWriterIfEnabled(runtime),
		emitter:          events.NewEmitter(events.GetBus()),
		pendingCreations: make(map[string]*PendingCreation),
		platformStats:    platformstats.New(),
	}, nil
}

// boxes returns the box-lifecycle backend. Production wires boxBackend in
// NewContainerServer; this lazily wraps the Manager when it's unset so a
// server constructed directly (legacy tests that set only `manager`) still
// routes lifecycle calls through the same LXC seam. The wrapper is a cheap
// struct over the Manager, so constructing it per call is fine.
func (s *ContainerServer) boxes() box.BoxBackend {
	if s.boxBackend != nil {
		return s.boxBackend
	}
	return boxlxc.New(s.manager)
}

// Boxes exposes the box backend so daemon wiring (dual_server) can
// type-assert runtime-specific capabilities — e.g. the K8s backend's
// ClientKeyLister for the /authorized-keys handler.
func (s *ContainerServer) Boxes() box.BoxBackend { return s.boxes() }

// k8sBoxes returns the box backend when the daemon runs the k8s runtime.
// Lifecycle handlers use it to route their action through the backend seam:
// s.manager is the LXC/incus surface, which is wired to an unavailable stub
// on the k8s runtime — so without this branch Start/Stop/Delete/Resize would
// error on every call there. The incus-config side effects those handlers
// stamp (autosleep timestamps, stopped_at, secrets re-stamping) are
// LXC-runtime features and are skipped on k8s.
func (s *ContainerServer) k8sBoxes() (box.BoxBackend, bool) {
	if bb := s.boxes(); bb.Kind() == box.KindK8s {
		return bb, true
	}
	return nil, false
}

// createTargetsPeer reports whether the create is bound for a remote backend,
// by the same rule as the forwarding block in createContainerAuthorized. It is
// evaluated AFTER pool placement has set req.BackendId, so a --pool create that
// placement routes to a peer is a peer-bound create too (#2952).
func (s *ContainerServer) createTargetsPeer(req *pb.CreateContainerRequest) bool {
	return req.BackendId != "" && s.peerPool != nil && req.BackendId != s.peerPool.LocalBackendID()
}

// validateStackOnPeer checks req.Stack against the catalog of the peer the
// create is bound for (#2952). It rejects only when it has read a non-empty
// catalog that lacks the stack. Anything it cannot conclude (no stack asked
// for, the peer unknown or unhealthy, the catalog unreadable or empty) is
// let through: the forwarding block reports an unreachable peer with its own
// error, and a peer validates every create it receives for itself, so this
// check exists to fail early, before storage is allocated, not to be the only
// line of defence.
func (s *ContainerServer) validateStackOnPeer(ctx context.Context, req *pb.CreateContainerRequest) error {
	if req.Stack == "" || s.peerPool == nil {
		return nil
	}
	peer := s.peerPool.Get(req.BackendId)
	if peer == nil || !peer.Healthy {
		return nil
	}
	ids, err := peer.ForwardListStacks(ctx, extractAuthToken(ctx))
	if err != nil {
		log.Printf("[create] could not read backend %q's stack catalog (%v); leaving the stack check to the backend", req.BackendId, err)
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if id == req.Stack {
			return nil
		}
	}
	sort.Strings(ids)
	return status.Errorf(codes.InvalidArgument, "unknown stack %q on backend %q; valid stacks there: %s",
		req.Stack, req.BackendId, strings.Join(ids, ", "))
}

// CreateContainer creates a new container
func (s *ContainerServer) CreateContainer(ctx context.Context, req *pb.CreateContainerRequest) (*pb.CreateContainerResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersWrite); err != nil {
		return nil, err
	}
	// Validate request
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if err := auth.AuthorizeTenant(ctx, req.Username); err != nil {
		return nil, err
	}
	return s.createContainerAuthorized(ctx, req)
}

// createContainerAuthorized is CreateContainer after its scope and tenant
// checks: the validation and provisioning body, for a caller that has
// already been authorised for req.Username at its own entry point. Today
// that is only the recipe deploy path (#2504): DeployRecipe checks
// containers:write, and a skill or crew run checks agents:run / crews:run,
// each with AuthorizeTenant on the very box name they then hand here. That
// is what lets an agents:run token provision the skill's own box — the
// contract scopes.go states for it — without also holding containers:write.
// Not a gRPC method: nothing on the wire reaches it.
func (s *ContainerServer) createContainerAuthorized(ctx context.Context, req *pb.CreateContainerRequest) (resp *pb.CreateContainerResponse, err error) {
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	// Namespace normalization must not turn a caller's reused dry-run request
	// into an apparently explicit assignment on the next call.
	if req.NetworkTenant == "" && auth.RequireRole(ctx, auth.RoleAdmin) != nil && s.nativeTenantLocalLXC() {
		req = proto.Clone(req).(*pb.CreateContainerRequest)
	}
	// Audit B-MED-1 / B-MED-2 / B-LOW-1: cap the unbounded
	// repeated-string / map fields before any allocation-heavy
	// work runs. Done after the tenant check (don't enumerate
	// resource caps to unauthenticated callers) but before pool
	// resolution and peer routing.
	if err := validateCreateContainerBounds(req); err != nil {
		return nil, err
	}
	// Pool resolution — if a pool is requested, either validate that
	// the explicit backend_id belongs to that pool, or pick any
	// healthy backend in the pool when backend_id is empty.
	//
	// This runs BEFORE the stack check and before any storage is allocated
	// (#2952): it only chooses a backend (it sets req.BackendId), it provisions
	// nothing, and the stack has to be checked against the catalog of the
	// backend that will actually create the box.
	if req.Pool != "" {
		if err := s.resolvePoolPlacement(req); err != nil {
			return nil, err
		}
	}
	if err := authorizeNetworkTenantCreate(ctx, req, s.nativeTenantLocalLXC() && !s.createTargetsPeer(req), accountTenancyArmed()); err != nil {
		return nil, err
	}
	if req.NetworkTenant != "" && (s.nativeTenantReady == nil || !s.nativeTenantReady()) {
		return nil, status.Error(codes.FailedPrecondition, "native network Tenant requires ready enforced local LXC Guard")
	}
	// Unknown stack (#2860, #2952): refuse before any allocation. The catalog
	// that counts is the one of the backend that will create the box: this
	// daemon's, or the peer's, which may define stacks this daemon does not.
	if s.createTargetsPeer(req) {
		if err := s.validateStackOnPeer(ctx, req); err != nil {
			return nil, err
		}
	} else if err := validateStackID(req.Stack); err != nil {
		return nil, err
	}
	// #1294's unconditional refusal used to sit here, on the grounds that the
	// create path did not provision an encrypted dataset. It now does
	// (#1341), so that statement is no longer true and the blanket refusal is
	// gone.
	//
	// What has NOT changed is the promise: validateEncryption below still
	// refuses an encrypted create on a daemon with no KeyProvider, which is
	// every OSS daemon until an operator configures one (#1342). That refusal
	// is the load-bearing one — it is what stops the flag being accepted and
	// silently ignored — and it is the last thing to go.
	// Birth TTL (#523): reject an out-of-range TTL before we provision
	// anything — fail fast rather than create a box and then reject its
	// death date. Same bound (7 days) as SetContainerTTL; 0 = no TTL.
	if err := validateTTLSeconds(req.TtlSeconds); err != nil {
		return nil, err
	}
	// Birth idle-stop (#524): a negative threshold is nonsense; reject early.
	// 0 = no auto-sleep; > 0 enables it with that idle threshold (minutes).
	if req.IdleStopMinutes < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "idle_stop_minutes must be >= 0, got %d", req.IdleStopMinutes)
	}
	// disable_auto_sleep (#2759) means "no idle-stop timer"; asking for one in
	// the same breath is a contradiction, not something to resolve by guessing.
	// A standalone daemon otherwise ignores the flag: it never enables
	// auto-sleep unless idle_stop_minutes > 0.
	if req.DisableAutoSleep && req.IdleStopMinutes > 0 {
		return nil, status.Error(codes.InvalidArgument, "disable_auto_sleep and idle_stop_minutes > 0 are contradictory: set one")
	}
	// Birth stopped→delete (#525): same — reject a negative window early.
	// 0 = never delete on stop; > 0 reaps a box left stopped that long.
	if req.DeleteAfterStoppedSeconds < 0 {
		return nil, status.Errorf(codes.InvalidArgument, "delete_after_stopped_seconds must be >= 0, got %d", req.DeleteAfterStoppedSeconds)
	}
	// Per-tenant dataset encryption (#1198): refuse before provisioning
	// anything. A create that accepts encrypted=true and then produces a
	// plaintext dataset is worse than a rejected one — the caller would
	// believe they had encryption at rest.
	if err := validateTenantID(req.TenantId); err != nil {
		return nil, err
	}
	if err := validateEncryption(req, s.keyProvider); err != nil {
		return nil, err
	}

	// Per-tenant encrypted storage (#1341). Resolved BEFORE anything is
	// provisioned: EnsureTenantStorage resolves the key first, so a key
	// custody outage fails here having created nothing.
	//
	// A tenant's containers share one Incus storage pool, sourced at that
	// tenant's encrypted dataset. Everything Incus creates inside it — images
	// and instances alike — inherits that tenant's key, which is the only way
	// an instance can be encrypted at all: Incus builds one by cloning the
	// image, and a ZFS clone takes its key from where it is made (#1335).
	var encPool string
	var encRef zfskey.KeyRef
	if req.GetEncrypted() && req.GetDryRun() {
		// Validate only (#2925): EnsureTenantStorage creates the tenant's
		// encrypted dataset and pool, which a dry run must not do. What it
		// would have refused for lack of wiring it is still refused for here,
		// with the same error as the real path below.
		if !s.encryption.enabled() {
			return nil, errEncryptionNotWired
		}
	} else if req.GetEncrypted() {
		encPool, encRef, err = s.encryption.EnsureTenantStorage(ctx, encryptionTenantFor(req))
		if err != nil {
			return nil, status.Error(codes.Unavailable, err.Error())
		}
		// A resolved-empty pool means encryption is half-wired: a KeyProvider
		// is configured but the hooks are not. Falling through would create
		// the container on the daemon's default pool — unencrypted, reported
		// as encrypted, which is precisely what #1294 refused to ship.
		if encPool == "" {
			return nil, errEncryptionNotWired
		}
	}

	// Route to peer if backend_id specifies a remote backend
	if req.BackendId != "" && s.peerPool != nil {
		localID := s.peerPool.LocalBackendID()
		if req.BackendId != localID && req.BackendId != "" {
			peer := s.peerPool.Get(req.BackendId)
			if peer == nil {
				return nil, fmt.Errorf("backend %q not found", req.BackendId)
			}
			if !peer.Healthy {
				return nil, fmt.Errorf("backend %q is not healthy", req.BackendId)
			}
			if req.GetDryRun() {
				// #2925: not forwarded. A peer that predates dry_run would
				// ignore it and really create the box. The stack was checked
				// against the peer's catalog above (#2952); the peer's other
				// checks are not run.
				return &pb.CreateContainerResponse{
					DryRun: true,
					Message: fmt.Sprintf("Validated by this daemon, and the stack against backend %q's catalog; that backend's "+
						"other checks (image policy, capacity) were not run.", req.BackendId),
				}, nil
			}
			// Forward to peer — extract auth token from context
			authToken := extractAuthToken(ctx)
			respBody, err := peer.ForwardCreateContainer(authToken, req)
			if err != nil {
				return nil, fmt.Errorf("failed to create container on backend %q: %w", req.BackendId, err)
			}
			return respBody, nil
		}
	}

	// Validate SSH keys at the API boundary to reject placeholder strings early
	for i, key := range req.SshKeys {
		if err := container.ValidateSSHPublicKey(key); err != nil {
			return nil, fmt.Errorf("ssh_keys[%d]: %w", i, err)
		}
	}

	// Audit B-HIGH-1: validate the image against the registry
	// allowlist before any runtime call. Empty allowlist accepts
	// everything (with a startup WARNING); a configured allowlist
	// rejects unknown registries.
	if err := validateImageRegistry(req.Image); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Phase 3.1 follow-up: when CONTAINARIUM_REQUIRE_IMAGE_DIGEST
	// is on, every image reference must carry an `@sha256:<64hex>`
	// suffix so the operator pins the exact image bytes. Disabled
	// by default — opt-in for supply-chain-paranoid deployments.
	if err := validateImageDigest(req.Image); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Phase 3.1 Phase-B: when CONTAINARIUM_VERIFY_IMAGE_DIGEST
	// is on, additionally verify the declared digest matches the
	// registry's published index for that alias. Catches
	// allowlisted-registry MITM and bytes-vs-declared-digest
	// divergence. Pre-pull → fail fast, no bandwidth wasted, no
	// state to clean up. See docs/security/IMAGE-DIGEST-VERIFY-DESIGN.md.
	if err := verifyImageDigestAgainstRegistry(ctx, req.Image); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// Audit A-HIGH-3: enable_podman=true implies privileged + apparmor=unconfined.
	// Gate that elevation behind CONTAINARIUM_PRIVILEGED_PODMAN_POLICY so
	// non-admin tenants don't auto-escalate just by setting the flag.
	enablePodmanPrivileged := false
	if req.EnablePodman {
		allowed, err := AuthorizePrivilegedPodman(ctx)
		if err != nil {
			return nil, err
		}
		enablePodmanPrivileged = allowed
	}

	// Build the runtime-neutral box spec
	spec := box.BoxSpec{
		Ref:                    box.BoxRef{Tenant: req.Username},
		NetworkTenant:          req.NetworkTenant,
		Image:                  req.Image,
		SSHKeys:                req.SshKeys,
		Labels:                 req.Labels,
		EnablePodman:           req.EnablePodman,
		EnablePodmanPrivileged: enablePodmanPrivileged,
		AutoStart:              true,
		Stack:                  req.Stack,
		StackParams:            req.StackParameters,
		OSType:                 req.OsType,
		Isolation:              req.Isolation,
		GPUs:                   req.Gpus, // legacy singular `gpu` no longer honored (#673)
		StaticIP:               req.StaticIp,
		// OTel app-monitoring opt-in. The daemon-level collector
		// endpoint is configured at startup via --otel-collector-
		// endpoint (or auto-discovered from the core OTel collector
		// LXC; configured by DualServer). OTelBackendID lets the
		// collector tag emissions with the originating VM for
		// cross-VM Grafana queries. Both are no-ops when
		// req.Monitoring is false.
		Monitoring:            req.Monitoring,
		OTelCollectorEndpoint: s.otelCollectorEndpoint,
		OTelBackendID:         s.localBackendID(),
		// Git source provisioning (optional) — the daemon fetches the
		// repo into the box at create time, no caller→box SSH.
		GitSource:     req.GitSource,
		GitRef:        req.GitRef,
		GitCredential: req.GitCredential,
		WorkspacePath: req.WorkspacePath,
		// Empty for an unencrypted create, which leaves placement exactly as
		// it was: the daemon-wide pool, or the default profile (#1339).
		StoragePool: encPool,
	}
	// Phase 2.5 follow-up — load the OTel bearer for
	// monitoring=true containers. Best-effort: an error
	// loading the bearer leaves OTelBearer empty, which
	// makes the env-stamping path skip the header (pre-2.5
	// behavior). Operators see the WARNING in the daemon log.
	if req.Monitoring {
		bearer, err := LoadOrCreateOTelBearer()
		if err != nil {
			log.Printf("[create] OTel bearer load failed: %v (header omitted; collector remains open)", err)
		}
		spec.OTelBearer = bearer
	}

	// Set resource limits
	if req.Resources != nil {
		spec.Resources.CPU = req.Resources.Cpu
		spec.Resources.Memory = req.Resources.Memory
		spec.Resources.Disk = req.Resources.Disk
		spec.Resources.StorageClass = req.Resources.StorageClass
		spec.Resources.MemoryRequest = req.Resources.MemoryRequest
		spec.Resources.CPURequest = req.Resources.CpuRequest
	}

	// Use defaults if not specified (os_type takes precedence in manager.go)
	if spec.Image == "" && spec.OSType == 0 {
		spec.Image = "images:ubuntu/24.04"
	}
	if spec.Resources.CPU == "" {
		spec.Resources.CPU = "4"
	}
	if spec.Resources.Memory == "" {
		spec.Resources.Memory = "4GB"
	}
	if spec.Resources.Disk == "" {
		spec.Resources.Disk = "50GB"
	}

	// CPU capacity admission (#1029 direction 2). Runs once the effective CPU
	// request is known and before any create sink (Box CR / async / sync), so
	// all three paths are gated uniformly. No-op unless an operator enabled the
	// gate; peer-routed creates already returned above and are gated by the
	// target peer's own daemon. See cpu_admission.go.
	//
	// #1588: an admitted request reserves its cores until cpuRelease runs, so
	// a concurrent admit against this host sees them as committed even
	// before this container actually exists. Released below — inline for
	// the Box-CR/sync paths (the top-level defer), inside the goroutine for
	// async, matching exactly how asyncHandledInline already splits
	// platformStats reporting between those two completion shapes.
	// Pool headroom floor (#3128): refuse before anything is allocated on a
	// pool past its floor. encPool is the tenant's pool for an encrypted
	// create and "" (the daemon's default pool) otherwise. An encrypted dry
	// run has not resolved its tenant pool, so there is no pool to check.
	if !req.GetEncrypted() || encPool != "" {
		if err := s.admitPoolHeadroom(encPool, poolcapacity.OperationCreate); err != nil {
			return nil, err
		}
	}

	cpuRelease, admitErr := s.admitCPUCapacity(req.Username, spec.Resources.CPU)
	if admitErr != nil {
		return nil, admitErr
	}

	// Validate only (#2925): every check above has passed and nothing has been
	// provisioned. Release the cores admission just reserved, or a dry run
	// would block real creates for the reservation TTL. Not counted as a
	// provisioning attempt (#1083), which starts below.
	if req.GetDryRun() {
		cpuRelease()
		return &pb.CreateContainerResponse{
			DryRun:  true,
			Message: fmt.Sprintf("Dry run: a create for %s would be accepted. Nothing was created.", req.Username),
		}, nil
	}

	// #1083: everything past this point is a genuine attempt to provision
	// a container — Box CR, async, or sync. Everything before it (auth,
	// request validation, pool/peer routing, admission) is a client-side
	// or routing rejection, not a provisioning attempt, and is
	// deliberately not counted.
	//
	// The defer covers the Box CR and sync paths uniformly via the named
	// return values (any of their many `return nil, err` statements below
	// sets `err` before this runs, exactly as if written by hand at each
	// site). The async path is different: its own synchronous return is
	// always "accepted" (CREATING, no error), not the real outcome — that
	// resolves later inside its background goroutine, which records its
	// own attempt via the same provisionStart. asyncHandledInline, set
	// once that goroutine is actually spawned, tells this defer to stand
	// down so the async case isn't double-counted.
	provisionStart := time.Now()
	asyncHandledInline := false
	defer func() {
		if asyncHandledInline {
			return
		}
		cpuRelease()
		s.platformStats.RecordProvisionAttempt(platformstats.OperationCreate, err == nil, time.Since(provisionStart))
	}()

	// Convergence (#995): when the Box operator is running, the imperative
	// create writes a Box CR and the reconciler builds the box — one builder
	// for both the imperative and declarative (kubectl apply / GitOps) paths.
	// Returns CREATING regardless of req.Async; the caller polls GET to track it.
	//
	// #1083: the top-level defer's success determination for this path means
	// "the Box CR was accepted by the k8s operator for reconciliation," not
	// "the box actually finished being built." The real reconciliation
	// outcome lives in the k8s Box operator's own status/events and is out
	// of scope for this PR — a future issue could feed that back into
	// platformStats once the operator exposes it.
	if s.boxWriter != nil {
		if err := s.boxWriter.Upsert(ctx, spec); err != nil {
			return nil, fmt.Errorf("create Box CR for %s: %w", req.Username, err)
		}
		return &pb.CreateContainerResponse{
			Container: &pb.Container{
				Name:     fmt.Sprintf("%s-container", req.Username),
				Username: req.Username,
				State:    pb.ContainerState_CONTAINER_STATE_CREATING,
				Resources: &pb.ResourceLimits{
					Cpu:           spec.Resources.CPU,
					Memory:        spec.Resources.Memory,
					Disk:          spec.Resources.Disk,
					StorageClass:  spec.Resources.StorageClass,
					MemoryRequest: spec.Resources.MemoryRequest,
					CpuRequest:    spec.Resources.CPURequest,
				},
			},
			Message: fmt.Sprintf("Box CR created for user %s; the operator is reconciling it. Poll GET /v1/containers/%s to check status.", req.Username, req.Username),
		}, nil
	}

	// Async mode - return immediately and create in background
	if req.Async {
		// Check if already creating
		s.pendingMu.Lock()
		if pending, exists := s.pendingCreations[req.Username]; exists && !pending.Done {
			s.pendingMu.Unlock()
			return nil, fmt.Errorf("container creation already in progress for user %s", req.Username)
		}

		// Track pending creation
		s.pendingCreations[req.Username] = &PendingCreation{
			Username:  req.Username,
			StartedAt: time.Now(),
		}
		s.pendingMu.Unlock()

		// Set provisioning callback
		spec.OnProvisioning = func() {
			s.pendingMu.Lock()
			if pending, exists := s.pendingCreations[req.Username]; exists {
				pending.Provisioning = true
			}
			s.pendingMu.Unlock()
		}

		// Start async creation. Use a background context — the request ctx is
		// cancelled once this handler returns the CREATING response.
		//
		// #1083: from here on the goroutine owns recording this attempt's
		// outcome (its real completion, not this handler's synchronous
		// "accepted" return) — stand the top-level defer down so it isn't
		// double-counted.
		asyncHandledInline = true
		go func() {
			// #1588: the top-level defer stood down for async (above), so
			// this goroutine owns releasing the CPU reservation too — on
			// every exit path, including the early "cancelled by delete"
			// return below, which is exactly why this is its own defer
			// rather than a call at the bottom of the function.
			defer cpuRelease()
			info, err := s.boxes().Create(context.Background(), spec)
			// Same contract as the sync path: an encrypted box whose
			// placement cannot be recorded is removed rather than left
			// unopenable. The caller already has its CREATING response, so
			// this surfaces through the pending state like every other
			// async failure.
			if err == nil {
				if rerr := s.recordEncryptedPlacement(req, info.Ref.Name, encRef, encPool); rerr != nil {
					err, info = rerr, nil
				}
			}

			// Phase 3.1 Phase-C: post-pull verification.
			// In async mode the HTTP response has already
			// returned with CREATING; mismatch detection
			// here can't reach the caller via the response
			// body, so we delete the container and record
			// the error in the pending state. The operator
			// polling for status sees a Done=true,
			// Error=<digest-mismatch> result.
			if err == nil && info != nil {
				if verr := verifyImageDigestPostPull(context.Background(), req.Image, info.Ref.Name, s.manager); verr != nil {
					if delErr := s.manager.Delete(req.Username, true); delErr != nil {
						log.Printf("[image-digest] async post-pull mismatch: failed to delete container %q: %v", info.Ref.Name, delErr)
					}
					err = verr
					info = nil
				}
			}

			// Birth TTL (#523), async path. The CREATING response already
			// returned, so a failure can't reach the caller via the response
			// body — it surfaces through the pending state (Done=true,
			// Error=<ttl>) the same way the digest-mismatch path above does.
			// stampBirthTTL deletes the box on failure so an ephemeral box
			// never leaks just because the async stamp lost a race.
			if err == nil && info != nil && req.TtlSeconds > 0 {
				if terr := s.stampBirthTTL(context.Background(), info.Ref.Name, req.Username, req.TtlSeconds); terr != nil {
					err = terr
					info = nil
				}
			}

			// Birth idle-stop (#524), async path. Best-effort like the sync
			// path — auto-sleep is an optimization, not a leak contract, so a
			// failed stamp never turns a created box into a failed creation.
			if err == nil && info != nil && req.IdleStopMinutes > 0 {
				s.stampBirthAutoSleep(info.Ref.Name, req.IdleStopMinutes)
			}
			// Birth stopped→delete (#525), async path. Best-effort like above.
			if err == nil && info != nil && req.DeleteAfterStoppedSeconds > 0 {
				s.stampBirthDeleteAfterStopped(info.Ref.Name, req.DeleteAfterStoppedSeconds)
			}

			// A delete that landed mid-provisioning owns this outcome, not
			// the creation (#1035): drop the tracking entry and log the one
			// line that describes what happened. Reporting it as a failure
			// is what let cleanup deletes masquerade as independent
			// confirmations of a create bug during cloud#920.
			s.pendingMu.Lock()
			pending, exists := s.pendingCreations[req.Username]
			cancelled := exists && pending.Cancelled
			switch {
			case cancelled:
				delete(s.pendingCreations, req.Username)
			case exists:
				pending.Done = true
				pending.Error = err
			}
			s.pendingMu.Unlock()

			if cancelled {
				log.Printf("Async container creation for %s cancelled by delete request (provisioning aborted; any error above is a consequence of the delete, not a creation failure)", req.Username)
				// The delete can land in the narrow window before the
				// instance exists, in which case it deleted nothing and
				// this goroutine went on to finish the box. Reap it —
				// otherwise the caller's delete silently leaves behind the
				// container it asked to remove.
				if err == nil && info != nil {
					if delErr := s.manager.Delete(req.Username, true); delErr != nil {
						log.Printf("Async container creation for %s completed after its delete; removing it failed: %v", req.Username, delErr)
					} else {
						log.Printf("Async container creation for %s completed after its delete; removed the late box", req.Username)
					}
				}
				return
			}

			// #1083: this is the REAL outcome of an async create — the
			// synchronous return below is only "accepted", not "succeeded"
			// (see asyncHandledInline above, which stood the top-level
			// defer down so this is the only place an async attempt gets
			// recorded). A cancelled-by-delete run already returned above
			// without reaching here, matching the existing #1035 rule that
			// a delete-cancelled create isn't a creation failure.
			s.platformStats.RecordProvisionAttempt(platformstats.OperationCreate, err == nil, time.Since(provisionStart))

			if err != nil {
				log.Printf("Async container creation failed for %s: %v", req.Username, err)
			}

			// Emit event on success
			if err == nil && info != nil {
				s.refreshContainerIPMap()
				s.emitter.EmitContainerCreated(toProtoContainer(info))
				// Same SSH-readiness push as the sync path (cloud #971).
				// It matters more here: this is the path the cloud control
				// plane uses, so this is the transition the caller sees as
				// CREATING → RUNNING and immediately tries to SSH into.
				// context.Background() because the request context died
				// with the CREATING response long before this ran.
				s.notifySentinelKeyChange(context.Background(), "create_container "+req.Username)
			}
		}()

		// Return immediately with CREATING state
		return &pb.CreateContainerResponse{
			Container: &pb.Container{
				Name:     fmt.Sprintf("%s-container", req.Username),
				Username: req.Username,
				State:    pb.ContainerState_CONTAINER_STATE_CREATING,
				Resources: &pb.ResourceLimits{
					Cpu:           spec.Resources.CPU,
					Memory:        spec.Resources.Memory,
					Disk:          spec.Resources.Disk,
					StorageClass:  spec.Resources.StorageClass,
					MemoryRequest: spec.Resources.MemoryRequest,
					CpuRequest:    spec.Resources.CPURequest,
				},
			},
			Message: fmt.Sprintf("Container creation started for user %s. Poll GET /v1/containers/%s to check status.", req.Username, req.Username),
		}, nil
	}

	// Sync mode - wait for completion
	info, err := s.boxes().Create(ctx, spec)
	if err != nil {
		if errors.Is(err, container.ErrNICGuard) {
			// Fail closed (tenant network guard): the box was removed
			// rather than started reachable by co-tenants.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Record which key unlocks this container and which pool it lives on,
	// before anything else touches it. See recordEncryptedPlacement: a
	// container whose placement cannot be recorded is deleted, because it is
	// encrypted and nothing can name its encryptionroot.
	if err := s.recordEncryptedPlacement(req, info.Ref.Name, encRef, encPool); err != nil {
		return nil, err
	}

	// Phase 3.1 Phase-C: post-pull defense-in-depth.
	// Confirm the image landed on disk matches the digest
	// the operator declared. Mismatch means cache tampering
	// or an index race — delete the just-created container
	// rather than leave the attacker's payload running.
	if err := verifyImageDigestPostPull(ctx, req.Image, info.Ref.Name, s.manager); err != nil {
		// Best-effort cleanup; the error we surface is the
		// digest mismatch, which is the load-bearing
		// signal. A failed delete is logged but doesn't
		// shadow the security event.
		if delErr := s.manager.Delete(req.Username, true); delErr != nil {
			log.Printf("[image-digest] failed to delete container %q after post-pull mismatch: %v", info.Ref.Name, delErr)
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// Birth TTL (#523): stamp the death date now that the box exists, before
	// any best-effort setup, so the ttlsweeper reaps it even if the client
	// dies the instant create returns. stampBirthTTL deletes the box and
	// errors if it can't honor the requested TTL — an ephemeral box that
	// would leak is worse than no box.
	if req.TtlSeconds > 0 {
		if err := s.stampBirthTTL(ctx, info.Ref.Name, req.Username, req.TtlSeconds); err != nil {
			return nil, err
		}
	}

	// Birth idle-stop (#524): enable auto-sleep at create with the requested
	// idle threshold, so the box is born with the stop timer and the
	// autosleep loop reclaims its CPU/RAM if a job crashes/cancels without
	// anyone calling toggle_auto_sleep. Best-effort: unlike the TTL (a leak
	// contract), auto-sleep is an optimization — a failed stamp logs and the
	// box keeps running (it can be toggled later), it never fails the create.
	if req.IdleStopMinutes > 0 {
		s.stampBirthAutoSleep(info.Ref.Name, req.IdleStopMinutes)
	}

	// Birth stopped→delete (#525): record the window so the ttlsweeper reaps
	// the box once it's been stopped that long. Best-effort like the other
	// lifecycle stamps. The clock only starts when the box actually stops
	// (StopContainer stamps stopped_at), so just persisting the window here
	// is enough.
	if req.DeleteAfterStoppedSeconds > 0 {
		s.stampBirthDeleteAfterStopped(info.Ref.Name, req.DeleteAfterStoppedSeconds)
	}

	// Deliver tenant secrets to the new box — env config on LXC, the
	// mounted Secret on K8s (best-effort; a failure here doesn't fail the
	// create, and RefreshSecrets is the retry). Runs after the box exists,
	// which on K8s matters: the Secret lands in the tenant namespace, and
	// that namespace is created with the box.
	if s.secretsStore != nil {
		if n, err := s.stampSecrets(ctx, req.Username); err != nil {
			log.Printf("[secrets] failed to stamp on %s: %v (continuing)", info.Ref.Name, err)
		} else if n > 0 {
			log.Printf("[secrets] stamped %d secret(s) on %s at create time", n, info.Ref.Name)
		}
	}

	// Refresh the collector's IP map so the new container's
	// app-emitted OTLP is attributed correctly. Best-effort.
	s.refreshContainerIPMap()

	// Convert to protobuf
	protoContainer := toProtoContainer(info)
	protoContainer.Pool = s.resolvePool(protoContainer.BackendId)
	protoContainer.SshHost = s.sshHost

	// Emit container created event
	s.emitter.EmitContainerCreated(protoContainer)

	// Push the new box's SSH key into the sentinel's sshpiper routing table
	// before returning, so RUNNING actually means SSH-reachable (cloud
	// #971). Without this the caller races a 2-minute keysync tick and its
	// first connections get `Permission denied (publickey)`.
	s.notifySentinelKeyChange(ctx, "create_container "+req.Username)

	resp = &pb.CreateContainerResponse{
		Container: protoContainer,
		Message:   fmt.Sprintf("Container %s created successfully", info.Ref.Name),
	}

	if ostype.IsWindows(req.OsType) {
		// Windows VM: return RDP address, skip jump server account
		resp.RdpAddress = protoContainer.RdpAddress

		// Register RDP connection in Guacamole (best-effort, runs in background)
		go func() {
			rdpPassword := info.Labels["rdp-password"]
			connID := s.registerGuacamoleConnection(info.Ref.Name, info.IPAddress, "Administrator", rdpPassword)
			if connID != "" {
				// Store connection ID as a label for cleanup on delete
				_ = s.manager.AddLabel(req.Username, guacamoleConnectionIDLabel, connID)
			}
		}()
	} else {
		// Linux container: return SSH command and ensure jump server account.
		resp.SshCommand = sshCommandFor(req.Username, protoContainer.SshHost, info.IPAddress)
		go func() {
			if err := container.EnsureJumpServerAccount(req.Username); err != nil {
				log.Printf("Warning: failed to create jump server account for %s: %v", req.Username, err)
				return
			}
			log.Printf("Jump server account ensured for %s", req.Username)

			// Write the create-request ssh_keys to the HOST-SIDE
			// authorized_keys (the jump-server account's file), the same
			// file AddSSHKey writes and ServeAuthorizedKeys serves to the
			// sentinel keysync. EnsureJumpServerAccount only creates the
			// account with an empty .ssh; the request keys were applied to
			// the LXC-internal authorized_keys (via Container.SSHKeys) but
			// NOT here — so a box created with ssh_keys was reachable on the
			// box itself yet REJECTED at the sentinel (publickey), because
			// sshpiper validates the client against the host-side file. Mirror
			// the request keys here so the sentinel authorizes exactly the
			// keys the box does. Keys are already validated above. (#470)
			// Each key written is also registered in ssh_keys (#2517).
			s.syncCreateRequestKeys(context.Background(), req.Username, req.SshKeys)
		}()
	}

	return resp, nil
}

// ListContainers lists all containers
func (s *ContainerServer) ListContainers(ctx context.Context, req *pb.ListContainersRequest) (*pb.ListContainersResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersRead); err != nil {
		return nil, err
	}
	// Tenant isolation: non-admin callers may only see their own
	// containers. Empty req.Username for a non-admin is rewritten to
	// the authenticated subject (was "list everyone's"); an explicit
	// different username is denied.
	subject, roles, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no authenticated subject")
	}
	if !auth.HasRole(roles, auth.RoleAdmin) {
		if req.Username != "" && req.Username != subject {
			return nil, status.Error(codes.PermissionDenied, "not authorized for this tenant")
		}
		req.Username = subject
	}

	// Snapshot in-flight async creations so list state is provisioning-aware
	// (#1036). GetContainer has reported CREATING/PROVISIONING from this map
	// since #837; list bypassed it, so a box ~30s into a multi-minute
	// provisioning run showed raw incus RUNNING to every list consumer
	// (control-plane state sync, CLI list) — inviting SSH long before the
	// box was actually ready. Failed creates (Done + Error) stay out of the
	// list on purpose: their instance is gone, and GetContainer owns
	// surfacing the ERROR until its cleanup.
	pendingStates := map[string]pb.ContainerState{}
	s.pendingMu.RLock()
	for u, p := range s.pendingCreations {
		if !p.active() {
			continue
		}
		st := pb.ContainerState_CONTAINER_STATE_CREATING
		if p.Provisioning {
			st = pb.ContainerState_CONTAINER_STATE_PROVISIONING
		}
		pendingStates[u] = st
	}
	s.pendingMu.RUnlock()

	// #1525: a K8s-backed daemon's manager wraps incus.UnavailableBackend,
	// which fails every call with "incus backend not available on this
	// host" — list has to dispatch through the runtime-neutral
	// box.BoxBackend seam the same way create/delete/get already do,
	// rather than assume Incus. box.BoxStatus already carries the
	// runtime-neutral IsCore flag in place of Role.IsCoreRole(), so
	// filtering mirrors the Incus path's logic exactly, just off the
	// neutral type.
	var protoContainers []*pb.Container
	if bb, isK8s := s.k8sBoxes(); isK8s {
		statuses, err := bb.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list containers: %w", err)
		}
		for i := range statuses {
			st := &statuses[i]
			if st.IsCore {
				continue
			}
			if req.Username != "" && st.Ref.Tenant != req.Username {
				continue
			}
			if len(req.LabelFilter) > 0 && !incus.MatchLabels(st.Labels, req.LabelFilter) {
				continue
			}
			pc := toProtoContainer(st)
			pc.Pool = s.resolvePool(pc.BackendId)
			pc.SshHost = s.sshHost
			s.withEncryptionState(ctx, pc)
			protoContainers = append(protoContainers, pc)
		}
	} else {
		containers, err := s.manager.List()
		if err != nil {
			return nil, fmt.Errorf("failed to list containers: %w", err)
		}

		// Filter containers. The state filter is NOT applied here: it must
		// match the provisioning-aware state the response reports (overlaid
		// below), not the raw incus state — otherwise state=RUNNING would
		// wrongly include a mid-provisioning box. applyProvisioningOverlay
		// re-applies it post-overlay for these local entries (peer entries
		// never went through the state filter — unchanged).
		var filtered []incus.ContainerInfo
		for _, c := range containers {
			// Exclude core containers (postgres, caddy) from user-facing listings
			if c.Role.IsCoreRole() {
				continue
			}

			// Filter by username if specified
			if req.Username != "" {
				// Extract username from container name (format: username-container)
				username := c.Name
				if len(c.Name) > 10 && c.Name[len(c.Name)-10:] == "-container" {
					username = c.Name[:len(c.Name)-10]
				}
				if username != req.Username {
					continue
				}
			}

			// Filter by labels if specified
			if len(req.LabelFilter) > 0 {
				if !incus.MatchLabels(c.Labels, req.LabelFilter) {
					continue
				}
			}

			filtered = append(filtered, c)
		}

		// Tag local containers with this daemon's backend ID
		if s.peerPool != nil && s.peerPool.LocalBackendID() != "" {
			for i := range filtered {
				filtered[i].BackendID = s.peerPool.LocalBackendID()
			}
		}

		// Convert to protobuf.
		for i := range filtered {
			st := boxlxc.StatusFromInfo(&filtered[i])
			pc := toProtoContainer(&st)
			pc.Pool = s.resolvePool(pc.BackendId)
			pc.SshHost = s.sshHost
			// Local boxes only. A peer's containers are described by the peer —
			// asking THIS daemon's hooks about them would answer from the wrong
			// key custody and report every remote container as unencrypted.
			s.withEncryptionState(ctx, pc)
			protoContainers = append(protoContainers, pc)
		}
	}

	protoContainers = applyProvisioningOverlay(protoContainers, pendingStates,
		req.Username, req.State, len(req.LabelFilter) > 0, s.sshHost)

	// Add containers from peer backends. A peer that couldn't be reached
	// contributes zero entries here — reported separately in
	// unreachable_backends so callers can tell "unreachable" from "this
	// backend genuinely has no containers" instead of both looking like a
	// bare absence (#1902, sibling of #1901's GetMetrics fix).
	var unreachableBackends []*pb.UnreachableBackend
	if s.peerPool != nil {
		authToken := extractAuthToken(ctx)
		peerContainers, unreachable := s.peerPool.ListContainers(authToken)
		for i := range peerContainers {
			st := boxlxc.StatusFromInfo(&peerContainers[i])
			pc := toProtoContainer(&st)
			pc.Pool = s.resolvePool(pc.BackendId)
			pc.SshHost = s.sshHost
			protoContainers = append(protoContainers, pc)
		}
		for _, u := range unreachable {
			unreachableBackends = append(unreachableBackends, &pb.UnreachableBackend{
				BackendId: u.BackendID,
				Reason:    u.Reason,
			})
		}
	}

	return &pb.ListContainersResponse{
		Containers:          protoContainers,
		TotalCount:          safecast.I32(len(protoContainers)),
		UnreachableBackends: unreachableBackends,
	}, nil
}

// applyProvisioningOverlay makes a list response honest about in-flight async
// creations (#1036), in three steps:
//
//  1. A local entry whose username has a pending creation reports that
//     pending state (CREATING/PROVISIONING) instead of the raw incus state —
//     incus says "Running" the moment the instance boots, minutes before
//     provisioning (package install, key setup) finishes and SSH works.
//  2. A pending creation with no incus instance yet gets a synthetic entry
//     (same shape GetContainer synthesizes), so a just-accepted create is
//     visible in list at all. Synthetics are skipped when a label filter is
//     in effect: a provisioning box's labels aren't stamped yet, so it can't
//     genuinely match any label filter.
//  3. The request's state filter is applied here, against the final overlaid
//     state (the caller removed it from the raw-incus filtering pass).
//
// Pure function over the already-converted local entries so the overlay is
// unit-testable without an incus-backed Manager.
func applyProvisioningOverlay(local []*pb.Container, pending map[string]pb.ContainerState,
	usernameFilter string, stateFilter pb.ContainerState, hasLabelFilter bool, sshHost string) []*pb.Container {

	out := make([]*pb.Container, 0, len(local)+len(pending))
	seen := make(map[string]bool, len(pending))
	for _, pc := range local {
		if st, ok := pending[pc.Username]; ok {
			pc.State = st
			seen[pc.Username] = true
		}
		if stateFilter != pb.ContainerState_CONTAINER_STATE_UNSPECIFIED && pc.State != stateFilter {
			continue
		}
		out = append(out, pc)
	}

	if hasLabelFilter {
		return out
	}
	for u, st := range pending {
		if seen[u] {
			continue
		}
		if usernameFilter != "" && u != usernameFilter {
			continue
		}
		if stateFilter != pb.ContainerState_CONTAINER_STATE_UNSPECIFIED && st != stateFilter {
			continue
		}
		out = append(out, &pb.Container{
			Name:     u + "-container",
			Username: u,
			State:    st,
			SshHost:  sshHost,
		})
	}
	return out
}

// GetContainer gets information about a specific container
func (s *ContainerServer) GetContainer(ctx context.Context, req *pb.GetContainerRequest) (*pb.GetContainerResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersRead); err != nil {
		return nil, err
	}
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if err := auth.AuthorizeTenant(ctx, req.Username); err != nil {
		return nil, err
	}

	// Check if there's a pending async creation
	s.pendingMu.RLock()
	pending, hasPending := s.pendingCreations[req.Username]
	s.pendingMu.RUnlock()

	if pending.active() {
		// Determine if creating or provisioning
		state := pb.ContainerState_CONTAINER_STATE_CREATING
		if pending.Provisioning {
			state = pb.ContainerState_CONTAINER_STATE_PROVISIONING
		}
		return &pb.GetContainerResponse{
			Container: &pb.Container{
				Name:     fmt.Sprintf("%s-container", req.Username),
				Username: req.Username,
				State:    state,
			},
		}, nil
	}

	if hasPending && pending.Done && pending.Error != nil {
		// Creation failed - return ERROR state with error details
		log.Printf("Async creation failed for %s: %v", req.Username, pending.Error)
		return &pb.GetContainerResponse{
			Container: &pb.Container{
				Name:     fmt.Sprintf("%s-container", req.Username),
				Username: req.Username,
				State:    pb.ContainerState_CONTAINER_STATE_ERROR,
			},
		}, nil
	}

	// Try to get from Incus
	info, err := s.boxes().Get(ctx, box.BoxRef{Tenant: req.Username})
	if err != nil || info == nil {
		// Not found locally — try peers
		if s.peerPool != nil {
			authToken := extractAuthToken(ctx)
			peerContainers, _ := s.peerPool.ListContainers(authToken)
			containerName := req.Username + "-container"
			for _, pc := range peerContainers {
				if pc.Name == containerName {
					st := boxlxc.StatusFromInfo(&pc)
					proto := toProtoContainer(&st)
					proto.Pool = s.resolvePool(proto.BackendId)
					proto.SshHost = s.sshHost
					return &pb.GetContainerResponse{
						Container: proto,
					}, nil
				}
			}
		}

		// If we had a pending creation that completed, clean it up
		if hasPending && pending.Done {
			s.pendingMu.Lock()
			delete(s.pendingCreations, req.Username)
			s.pendingMu.Unlock()
		}
		if err == nil {
			err = fmt.Errorf("container not found")
		}
		return nil, fmt.Errorf("failed to get container: %w", err)
	}

	// Clean up pending creation if exists
	if hasPending {
		s.pendingMu.Lock()
		delete(s.pendingCreations, req.Username)
		s.pendingMu.Unlock()
	}

	protoInfo := toProtoContainer(info)
	protoInfo.Pool = s.resolvePool(protoInfo.BackendId)
	protoInfo.SshHost = s.sshHost
	// #1202: surface KEY_UNAVAILABLE before someone trips over it. ZFS lets
	// snapshots be taken with the key unloaded and refuses only to read them
	// back, so without this a tenant accumulates unrestorable snapshots and
	// nothing says so until a restore is attempted.
	s.withEncryptionState(ctx, protoInfo)
	return &pb.GetContainerResponse{
		Container: protoInfo,
		// TODO: Add metrics
	}, nil
}

// DeleteContainer deletes a container
func (s *ContainerServer) DeleteContainer(ctx context.Context, req *pb.DeleteContainerRequest) (resp *pb.DeleteContainerResponse, err error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersWrite); err != nil {
		return nil, err
	}
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if err := auth.AuthorizeTenant(ctx, req.Username); err != nil {
		return nil, err
	}

	containerName := fmt.Sprintf("%s-container", req.Username)

	// #1083: everything past this point is a genuine attempt to delete a
	// container — k8s, peer-forwarded, or local-manager. Auth/validation
	// above is a client-side rejection, not a provisioning attempt, and is
	// deliberately not counted. Unlike CreateContainer, delete has no async
	// path — every branch below resolves synchronously, so one defer over
	// the named returns covers all three uniformly.
	provisionStart := time.Now()
	defer func() {
		s.platformStats.RecordProvisionAttempt(platformstats.OperationDelete, err == nil, time.Since(provisionStart))
	}()

	// Claim any in-flight async creation before touching the instance (#1035).
	// Provisioning takes minutes, so a delete very often lands mid-run; without
	// this the goroutine reports the resulting "Instance not found" as
	// "Async container creation failed", which reads in the journal exactly
	// like a genuine create bug. Marking first (not after) matters: the
	// misleading exec errors are produced by the delete itself.
	cancelledCreate := s.cancelPendingCreation(req.Username)
	if cancelledCreate {
		log.Printf("Delete for %s arrived while its creation was still provisioning; cancelling the create", req.Username)
	}

	// Before deleting, deregister Guacamole connection if this is a Windows VM
	s.deregisterGuacamoleConnection(req.Username)

	if bb, isK8s := s.k8sBoxes(); isK8s {
		// K8s runtime: deleting the Sandbox cascades to its pod + Service via
		// owner refs; the backend keeps the namespace + data PVC (its
		// delete-retains-data contract) and removes the gateway Pipe + Secrets.
		if err := bb.Delete(ctx, box.BoxRef{Tenant: req.Username}, req.Force); err != nil {
			s.uncancelPendingCreation(req.Username, cancelledCreate)
			return nil, fmt.Errorf("failed to delete container: %w", err)
		}
		s.cascadeContainerCleanup(ctx, containerName, req.Username)
		s.emitter.EmitContainerDeleted(containerName)
		// Retire the box's sshpiper pipe now, not on the next tick — see
		// the LXC delete path below for why (cloud #971).
		s.notifySentinelKeyChange(ctx, "delete_container "+req.Username)
		return &pb.DeleteContainerResponse{
			Message:       fmt.Sprintf("Container for user %s deleted successfully", req.Username),
			ContainerName: containerName,
		}, nil
	}

	err = s.manager.Delete(req.Username, req.Force)
	if err != nil {
		// Not found locally — try peers
		if s.peerPool != nil {
			authToken := extractAuthToken(ctx)
			peer, unreachablePeers := s.peerPool.FindContainerPeer(req.Username, authToken)
			if peer != nil {
				forceParam := ""
				if req.Force {
					forceParam = "?force=true"
				}
				_, statusCode, fwdErr := peer.ForwardRequest("DELETE", fmt.Sprintf("/v1/containers/%s%s", req.Username, forceParam), authToken, nil)
				if fwdErr != nil {
					s.uncancelPendingCreation(req.Username, cancelledCreate)
					return nil, fmt.Errorf("failed to delete container on peer %s: %w", peer.ID, fwdErr)
				}
				if statusCode >= 400 {
					s.uncancelPendingCreation(req.Username, cancelledCreate)
					return nil, fmt.Errorf("peer %s returned status %d for delete", peer.ID, statusCode)
				}
				return &pb.DeleteContainerResponse{
					Message:       fmt.Sprintf("Container for user %s deleted on backend %s", req.Username, peer.ID),
					ContainerName: containerName,
				}, nil
			}
			// #1905: the container may genuinely be nowhere, or it may be
			// sitting on one of these unreachable backends — reporting
			// "deleted"/"not found" here would be a false positive/negative
			// on a real container.
			if len(unreachablePeers) > 0 {
				s.uncancelPendingCreation(req.Username, cancelledCreate)
				return nil, fmt.Errorf("cannot confirm delete for %s: %d backend(s) unreachable (%s)",
					req.Username, len(unreachablePeers), unreachablePeers[0].Reason)
			}
		}
		s.uncancelPendingCreation(req.Username, cancelledCreate)
		return nil, fmt.Errorf("failed to delete container: %w", err)
	}

	// Cascade-clean the routes + TLS subjects + host user that this
	// container owned. The LXC is gone above; without these steps the
	// public hostname returns 502 (Caddy route still points at a
	// deleted upstream IP) and Caddy keeps trying to ACME-renew an
	// orphaned cert. All best-effort: any single failure logs a
	// warning but doesn't block the response — the LXC delete already
	// succeeded, and partial-cascade is better than telling the caller
	// "delete failed" when the container is in fact gone.
	s.cascadeContainerCleanup(ctx, containerName, req.Username)

	// Emit container deleted event
	s.emitter.EmitContainerDeleted(containerName)

	// Drop the box's pipe from the sentinel's routing table now rather than
	// leaving it live until the next tick (cloud #971 is the same clock,
	// read from the other end: a deleted box's key stays routable for up to
	// two minutes). Apply()'s stale-dir prune (#835) does the removal; this
	// just stops it waiting on the timer.
	s.notifySentinelKeyChange(ctx, "delete_container "+req.Username)

	// Refresh the collector's IP map so the deleted container's IP
	// is no longer claimed in source-IP attribution.
	s.refreshContainerIPMap()

	return &pb.DeleteContainerResponse{
		Message:       fmt.Sprintf("Container for user %s deleted successfully", req.Username),
		ContainerName: containerName,
	}, nil
}

// cancelPendingCreation flags an in-flight async creation for this user as
// cancelled-by-delete and reports whether it found one (#1035). The creating
// goroutine can't be aborted mid-step — it's deep inside an image launch or a
// package install — so cancellation is about attribution, not interruption:
// the goroutine reads the flag on completion and logs a cancellation instead
// of a failure, and Get/List stop reporting CREATING for a box that is being
// torn down.
func (s *ContainerServer) cancelPendingCreation(username string) bool {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	pending, exists := s.pendingCreations[username]
	if !exists || pending.Done || pending.Cancelled {
		return false
	}
	pending.Cancelled = true
	return true
}

// uncancelPendingCreation reverts a cancellation whose delete then failed, so
// a creation that is still genuinely running keeps reporting its real state.
// No-op unless this delete is the one that set the flag.
func (s *ContainerServer) uncancelPendingCreation(username string, cancelled bool) {
	if !cancelled {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if pending, exists := s.pendingCreations[username]; exists {
		pending.Cancelled = false
	}
