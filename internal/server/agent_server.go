package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coderun/engine"
	"github.com/footprintai/containarium/internal/modelgateway"
	"github.com/footprintai/containarium/internal/netpolicy"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/tracker"
	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	// Aliased: this file's RunAgentSkill/provisionSkillBox already use
	// "container" as a local variable name (the *pb.Container being built).
	containerpkg "github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/footprintai/containarium/pkg/version"
)

// agentBoxPrefix is the box/tenant name prefix RunAgentSkill assigns to a
// skill's box (agent-<skill-id>). The minted in-box JWT carries this as its
// subject, so a call originating from an agent box authenticates as
// agent-<skill-id> — which is how SendAgentTask recovers the real caller.
const agentBoxPrefix = "agent-"

// agentTokenTTL bounds the lifetime of the JWT minted for a skill's in-box
// agent loop. Short by design: a skill run is a bounded task, not a session.
const agentTokenTTL = 30 * time.Minute

// agentSeedRoot and agentWorkspaceRoot are the fixed parents of every run's
// own seed directory and (when it fetches a repo, #1859) workspace inside the
// box. Per-run (#1860): before this, every run of a skill shared one seed
// directory at the box level, so two concurrent runs of the same skill — or a
// crew member / queue worker sharing the box with a one-off run — collided on
// each other's prompt, token, and (once #1859 landed) checkout. seedDirFor and
// workspaceDirFor turn a run id, already validated by resolveRunID as a
// single safe path segment, into that run's own directory under these roots.
// The in-box agent loop (the agent-runtime image's job) reads its seed from
// AGENT_SEED_DIR, set to seedDirFor's result at launch.
const (
	agentSeedRoot      = "/etc/containarium/agent/runs"
	agentWorkspaceRoot = "/workspace/runs"
)

// seedDirFor returns the per-run seed directory for runID.
func seedDirFor(runID string) string { return agentSeedRoot + "/" + runID }

// workspaceDirFor returns the per-run workspace directory for runID.
func workspaceDirFor(runID string) string { return agentWorkspaceRoot + "/" + runID }

// workspaceSeed is the Go side of the daemon<->runtime workspace contract
// (design doc §5, coding-skill-on-a-repo, cloud repo). Written as
// <seed>/workspace.json whenever a run fetched a git source, so the in-box
// runtime can bind its file tools to the checkout (AGENTBOX_ROOT) and cite
// the commit it is working from, without re-deriving either from the run id.
type workspaceSeed struct {
	Path      string `json:"path"`
	GitSource string `json:"git_source"`
	GitRef    string `json:"git_ref"`
	GitCommit string `json:"git_commit"`
}

// workspacePromptNote is appended to the run's system prompt when a checkout
// exists. The in-box runtime does not read workspace.json yet, so its file
// tools root at the box user's HOME and the agent burns tool calls hunting for
// a repository the daemon already cloned (#2512).
func workspacePromptNote(seed workspaceSeed) string {
	note := "\n\nYour repository checkout is at " + seed.Path
	if seed.GitCommit != "" {
		note += " (commit " + seed.GitCommit + ")"
	}
	return note + ". Relative paths in the task are relative to that directory; " +
		"use absolute paths under it with the file tools, and do not search for the repository."
}

// buildWorkspaceSeedScript writes seed as JSON into seedDir/workspace.json,
// and appends workspacePromptNote to seedDir/system_prompt.txt.
// Mirrors buildAgentSeedScript's shape (mkdir -p, single-quoted printf) since
// every field here — a caller-supplied repo URL and ref included — needs the
// same shell-injection guard that function already carries.
func buildWorkspaceSeedScript(seedDir string, seed workspaceSeed) string {
	raw, err := json.Marshal(seed)
	if err != nil {
		// workspaceSeed has no field that can fail to marshal (four plain
		// strings) — reachable only if that ever changes, and a marshal
		// failure here must not crash the run: a workspace.json write is a
		// nicety for a future runtime, not a correctness requirement for the
		// fetch that already succeeded.
		raw = []byte(`{}`)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mkdir -p %s\n", seedDir)
	fmt.Fprintf(&b, "printf '%%s' %s > %s/workspace.json\n", shellSingleQuote(string(raw)), seedDir)
	if seed.Path != "" {
		fmt.Fprintf(&b, "printf '%%s' %s >> %s/system_prompt.txt\n", shellSingleQuote(workspacePromptNote(seed)), seedDir)
	}
	return b.String()
}

// Run-lease reasons, recorded on every revocation and in the end audit row so
// an operator reading jwt_revocations can tell a normal run exit from a run
// that never got off the ground.
const (
	runExitReason         = "run_exit"
	provisionFailedReason = "provision_failed"
	// dispatchSweepReason: the tracker dispatcher's sweep ended the run's
	// lease because it outlived the connection's run timeout (#2026).
	dispatchSweepReason = "dispatch_sweep"
	// dispatchEndedBeforeLaunchReason: the run finished provisioning after
	// its dispatch row had already been failed (the sweep timed it out
	// mid-provision), so it was torn down before its agent launched.
	dispatchEndedBeforeLaunchReason = "dispatch_ended_before_launch"
)

// endRunLeaseCeiling is the design's 6s cap on the REVOCATION half of ending a
// lease: runlease.End derives each credential's 2s revoke context from the one
// endRunLease passes in, so this bounds them together.
//
// It is NOT a bound on runlease.End as a whole, and the arithmetic is worth
// stating exactly because it is easy to assume otherwise. runlease.wipeSeed
// and (#1860) runlease's directory removal each take no context at all —
// they enforce their own 3s with a bare timer apiece — so the true worst case
// is 2s + 2s + 3s + 3s = 10s, and with two credentials the 4s of revokes
// never reach this 6s cap in the first place.
//
// That the wipe/removal steps do not derive from this deadline is the
// property that keeps it safe: slow revokes can never truncate them, so the
// seed files (and, once fetched, the workspace) still leave the box. Anyone
// making wipeSeed or the directory removal context-aware must raise this
// ceiling (or give that step its own budget) in the same change — otherwise
// slow revokes would silently cut a later step short and leave a readable
// `token` / `gateway.env` — or a fetched repo — behind in a box that is
// reused across runs.
const endRunLeaseCeiling = 6 * time.Second

// auditWriteBudget bounds one run-lease audit write.
//
// It has to exist separately from endRunLeaseCeiling: audit.Store.Log opens a
// transaction and takes a GLOBAL pg_advisory_xact_lock with no timeout of its
// own, deliberately serialising every audit appender against every other. The
// run-lease rows are written on a context detached from the caller's, so
// without a deadline here a reachable-but-wedged Postgres — or plain contention
// from another long audit transaction — would block RunAgentSkill inside its
// own deferred cleanup for as long as the database took. A dropped audit row is
// recoverable; a hung RPC is not.
const auditWriteBudget = 3 * time.Second

// runIDPattern is the accepted shape of a caller-supplied run id. Deliberately
// narrow: a run id is caller data that lands in JWT claims and audit rows, so
// it gets a character class with no shell, SQL, or JSON significance. It is
// never interpolated into a shell command.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// resolveRunID validates a caller-supplied run id or generates one. Empty
// means "the daemon picks"; anything else must match runIDPattern. "." and
// ".." both match that character class but are rejected explicitly (#1860):
// a run id becomes the single trailing path segment of the run's seed dir
// and workspace (seedDirFor/workspaceDirFor), and either value would resolve
// to the root itself rather than a per-run directory under it.
func resolveRunID(raw string) (string, error) {
	if raw == "" {
		return uuid.NewString(), nil
	}
	if raw == "." || raw == ".." {
		return "", status.Errorf(codes.InvalidArgument, "run_id must not be %q", raw)
	}
	if !runIDPattern.MatchString(raw) {
		return "", status.Errorf(codes.InvalidArgument,
			"run_id must match %s (1-128 chars of letters, digits, dot, underscore, hyphen)", runIDPattern.String())
	}
	return raw, nil
}

// AgentSkillServer implements the gRPC AgentSkillService (Phase 0:
// agent-as-a-box). It is pure orchestration: RunAgentSkill resolves a skill
// from the catalog, provisions its box by reusing RecipeServer.deploy, mints a
// JWT scoped to exactly the skill's allowed_scopes, and seeds the box. The
// in-box agent loop that consumes the seed and produces an artifact is the
// agent-runtime image's responsibility and is intentionally out of scope for
// Phase 0 (artifact_json is returned empty until that lands).
type AgentSkillServer struct {
	graphPeers sync.Map // child run id -> immutable graphPeerSet
	// gatewayCalls is the model gateway's per-run call log (run_log.go); nil
	// returns no calls.
	gatewayCalls gatewayCallReader
	pb.UnimplementedAgentSkillServiceServer
	catalog   *skills.Manager
	recipes   *RecipeServer        // box provisioning (reuses CreateContainer/exec/expose)
	tokens    *auth.TokenManager   // mints the skill's scoped in-box token
	netpolicy *NetworkPolicyServer // compiles allowed_peers into a per-box egress policy (Phase 2)
	audit     auditLogger          // records A2A hops and run leases; set once the pool is ready
	gateway   *gatewayProvisioning // model-gateway provisioning (#674); nil ⇒ boxes run in direct mode
	queue     AgentTaskQueue       // pull-based run queue (#674) — Enqueue/Lease/Complete
	// revocations kills a run's credentials when the run ends (#1817). nil on a
	// daemon without Postgres: runs still complete and seed files are still
	// wiped, but the credentials live to their expiry and each run says so in
	// the log.
	revocations runlease.Revoker
	// runs is the in-memory registry of currently-live runs (#1922) —
	// nil until dual_server.go wires the same instance into both this
	// server and ContainerServer via SetRunRegistry. Registration is
	// nil-guarded at each call site rather than inside Registry itself,
	// matching this file's existing convention for audit/revocations.
	runs *runlease.Registry
	// dispatched tracks the tracker-dispatched runs this daemon holds
	// (#2026), from StartRun until their end has been reported, so the
	// dispatcher's sweep can tell a live run from a lost one and end a
	// timed-out run's lease exactly once. Zero value is ready to use.
	dispatched dispatchedRuns
	// platformMCPPort is the daemon HTTP port an in-box platform MCP dials
	// (#1922 D4). Zero (the default, and every daemon that hasn't wired it)
	// means no run is given tracker tools: platformMCPSeedScript refuses to
	// write a URL it cannot make usable.
	platformMCPPort int
	// trackerConnections validates a caller-supplied
	// RunAgentSkillRequest.tracker_connection against the caller's own
	// tenant before it's minted into the run's JWT (#1922 step 6, design
	// note decision D3). Nil on a daemon without the tracker store wired
	// (--standalone, or Postgres unavailable): a request naming a
	// tracker_connection then fails closed rather than minting an
	// unvalidated claim — see RunAgentSkill.
	trackerConnections trackerConnectionChecker
	// lineageReservations sweeps a run's leftover fan-out reservations
	// when its lease ends (#2062). Nil on a daemon without the tracker
	// store wired: there is nothing to sweep.
	lineageReservations lineageReservationReleaser
	// Run journal read path (#2096). runIndex remembers each run's member
	// skills for TailRunLog; crewRunMembers resolves a crew run from its
	// durable record (wired by NewCrewServer); execScript is a test seam over
	// the container manager; runLogPoll overrides the follow poll interval;
	// journalRetention is how long a run's journal is kept on a member box.
	//
	// recordSkillRun and reapSkillRun (#2122) extend the same durable-record
	// path to a standalone skill run: recordSkillRun is called once a skill
	// run's box is provisioned, reapSkillRun once its journal directory is
	// reaped, both wired by NewCrewServer onto the same store crewRunMembers
	// already reads from.
	runIndex         runMemberIndex
	crewRunMembers   crewRunMembersFunc
	recordSkillRun   recordSkillRunFunc
	reapSkillRun     reapSkillRunFunc
	execScript       boxScriptFunc
	runLogPoll       time.Duration
	journalRetention time.Duration

	// boxOps overrides the container manager for a run's seed, git fetch,
	// workspace seed and lease wipe (#2161). Nil in production; see
	// agentBoxOps.
	boxOps agentBoxOps
	// inBoxOps overrides the container manager for the in-box agent-runtime
	// exec and the artifact.json read. Nil in production; see inBoxRunner.
	inBoxOps inBoxRunner
	// serveReadyTimeout and serveReadyInterval bound how long startServeMode
	// waits for a member's A2A endpoint to answer after launching it (#2936).
	// Zero means serveReadyTimeoutDefault / serveReadyIntervalDefault; tests set
	// them to milliseconds.
	serveReadyTimeout  time.Duration
	serveReadyInterval time.Duration
}

// inBoxRunner is the part of *container.Manager runInBoxAgentResult goes
// through: the agent-runtime exec (a non-zero exit reported as data, so a
// missing runtime is told apart from a failed run) and the artifact read.
type inBoxRunner interface {
	ExecWithExitCode(container string, cmd []string) (stdout, stderr string, exitCode int, err error)
	ReadFile(container, path string) ([]byte, error)
}

var _ inBoxRunner = (*containerpkg.Manager)(nil)

// agentBoxOps is the part of *container.Manager that provisionSkillBoxWith's
// seed / git-fetch / workspace steps and the run-lease wipe go through.
// *container.Manager satisfies it. It exists as a test seam, like execScript
// for TailRunLog: (*container.Manager).Exec only works against a real incus
// client, so without it no unit test gets past the seed to the fetch.
type agentBoxOps interface {
	Exec(container string, cmd []string) error
	FetchGitSource(container string, spec containerpkg.GitSourceSpec) (string, error)
}

var _ agentBoxOps = (*containerpkg.Manager)(nil)

// agentBox returns the boxOps override, else the container manager.
func (s *AgentSkillServer) agentBox() agentBoxOps {
	if s.boxOps != nil {
		return s.boxOps
	}
	return s.recipes.containers.manager
}

// trackerConnectionChecker is the one method of *tracker.Store
// RunAgentSkill needs, narrowed the same way auditLogger narrows
// *audit.Store — so the tenant-ownership check is testable without a
// real Postgres-backed tracker.Store. *tracker.Store satisfies this
// directly.
type trackerConnectionChecker interface {
	Get(ctx context.Context, username, name string) (*tracker.Connection, error)
}

// SetRunRegistry wires the shared in-memory run registry (#1922) — the
// SAME instance dual_server.go also gives to ContainerServer, so a
// tracker write RPC can resolve run_id -> skill/model and ask "is this
// run still live" for a run this server created.
func (s *AgentSkillServer) SetRunRegistry(r *runlease.Registry) {
	s.runs = r
}

// SetPlatformMCPPort wires the daemon HTTP port an in-box platform MCP dials
// (#1922 D4). Called from dual_server.go with the same port the model gateway
// uses; the host half is resolved in-box from the default route.
func (s *AgentSkillServer) SetPlatformMCPPort(port int) {
	s.platformMCPPort = port
}

// SetTrackerConnections wires the tracker-connections store (#1922 step
// 6) so RunAgentSkill can validate a tracker_connection request field
// against the caller's own tenant before minting it into the run's JWT.
// Nil (the default) makes any request naming a tracker_connection fail
// closed with FailedPrecondition.
func (s *AgentSkillServer) SetTrackerConnections(c trackerConnectionChecker) {
	s.trackerConnections = c
}

// lineageReservationReleaser is the one method of *tracker.Store
// endRunLease needs (#2062), narrowed like trackerConnectionChecker.
type lineageReservationReleaser interface {
	ReleaseRunReservations(ctx context.Context, username, runID string) (int64, error)
}

// SetLineageReservations wires the tracker store so a run's leftover
// fan-out reservations are swept when its lease ends (#2062). Nil (the
// default) leaves them in place. As with SetRevocationStore, the caller
// must pass a true nil, not a nil *tracker.Store.
func (s *AgentSkillServer) SetLineageReservations(r lineageReservationReleaser) {
	s.lineageReservations = r
}

// auditLogger is the one method of *audit.Store this server uses. Narrowed to
// an interface so what actually lands in audit_logs — the action, the resource
// type, the run id column — is assertable in a unit test; *audit.Store itself
// needs a live Postgres pool, which is why the A2A hop path was only ever
// smoke-tested.
type auditLogger interface {
	Log(ctx context.Context, entry *audit.AuditEntry) error
}

// SetAuditStore wires the audit store once the Postgres pool exists (it isn't
// available at construction). A2A hop and run-lease logging no-op until then.
//
// The nil check is load-bearing now that the field is an interface: a nil
// *audit.Store assigned straight into it would arrive as a non-nil interface
// holding a nil pointer, and the `s.audit == nil` guards below would stop
// firing. Same hazard as SetRevocationStore, but caught here because this
// setter takes the concrete type.
func (s *AgentSkillServer) SetAuditStore(store *audit.Store) {
	if store == nil {
		s.audit = nil
		return
	}
	s.audit = store
}

// SetRevocationStore wires the jti revocation store so a run's credentials can
// be killed when the run ends (#1817), mirroring SetAuditStore: it isn't
// available at construction, and revocation no-ops until it is.
//
// The CALLER must pass a true nil when it has no store. A nil
// *auth.PgRevocationStore handed in here arrives as a NON-nil interface holding
// a nil pointer, which no `== nil` check in this package or in runlease can
// see, and the first revoke of the first run then dereferences nil.
// dual_server.go does that nil check at the one place the concrete pointer
// exists.
func (s *AgentSkillServer) SetRevocationStore(store auth.RevocationStore) {
	s.revocations = store
}

// SetGatewayProvisioning enables model-gateway provisioning for skill boxes:
// each provisioned box gets a per-skill gateway token + the SDK base-URL env so
// its model calls route through the daemon-served gateway (key custody +
// metering). Wired from dual_server when a provider key is configured; nil-safe
// (no call ⇒ direct mode).
//
// defaultProvider is what an UNSPECIFIED-engine skill resolves to (today's
// gatewayPrimaryProvider(keys)); httpPort the daemon HTTP port the box dials;
// secret the shared jwt secret; hostIP the bridge gateway IP the box reaches
// the gateway/daemon/DNS on (used to pin skill-box egress to the gateway,
// #674 inc 4; empty disables pinning).
//
// globalProviders and keyResolver (#2222) are agentengine.Resolve's view of
// which providers are ready: globalProviders is the set the daemon holds an
// env/global key for (keys from dual_server's gatewayRegistryFromEnv, not the
// key values themselves); keyResolver checks a per-owner key when a provider
// isn't in that set — nil when the daemon has no secrets store (no Postgres),
// which just means only globalProviders can make a provider ready.
//
// models (#2229) is the real gateway's ListModels, for validating a named-
// engine skill's pinned model against what the resolved provider actually
// serves before minting its token. nil is a legitimate value (direct mode,
// or a caller not wired for this check) — provisionSkillBoxWith treats a nil
// models the same as "can't check", never as a refusal.
// SetNoGlobalKeyFallback mirrors the gateway's DisableGlobalKeyFallback into
// engine readiness, so a run whose owner has no key is refused up front rather
// than admitted on a daemon-global key the gateway will not spend. Call after
// SetGatewayProvisioning; a no-op in direct mode.
func (s *AgentSkillServer) SetNoGlobalKeyFallback(on bool) {
	if s.gateway != nil {
		s.gateway.engines.NoGlobalFallback = on
	}
}

func (s *AgentSkillServer) SetGatewayProvisioning(defaultProvider string, httpPort int, secret []byte, hostIP string, globalProviders map[string]bool, keyResolver modelgateway.KeyResolver, models modelLister) {
	g := &gatewayProvisioning{
		engines: agentengine.Gateway{
			DefaultProvider: defaultProvider,
			GlobalProviders: globalProviders,
			Keys:            keyResolver,
		},
		httpPort: httpPort,
		secret:   secret,
		models:   models,
	}
	if hostIP = strings.TrimSpace(hostIP); hostIP != "" {
		g.egressCIDR = hostIP + "/32"
	}
	s.gateway = g
}

// NewAgentSkillServer wires the agent-skill service to the recipe server (for
// box provisioning), the token manager (for minting scoped in-box tokens), and
// the network policy server (to compile allowed_peers into a per-box egress
// policy at launch). netpolicy may be nil — policy compilation then no-ops.
func NewAgentSkillServer(recipes *RecipeServer, tokens *auth.TokenManager, netpolicy *NetworkPolicyServer) *AgentSkillServer {
	return &AgentSkillServer{
		catalog:   skills.GetDefault(),
		recipes:   recipes,
		tokens:    tokens,
		netpolicy: netpolicy,
		queue:     NewMemAgentTaskQueue(),
	}
}

// ListAgentSkills returns all built-in skills.
func (s *AgentSkillServer) ListAgentSkills(ctx context.Context, _ *pb.ListAgentSkillsRequest) (*pb.ListAgentSkillsResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRead); err != nil {
		return nil, err
	}
	return &pb.ListAgentSkillsResponse{Skills: s.catalog.List()}, nil
}

// GetAgentSkill returns a single skill by ID.
func (s *AgentSkillServer) GetAgentSkill(ctx context.Context, req *pb.GetAgentSkillRequest) (*pb.GetAgentSkillResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRead); err != nil {
		return nil, err
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	skill, err := s.catalog.Get(req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &pb.GetAgentSkillResponse{Skill: skill}, nil
}

// ProvisionSkillBox creates or reuses a skill's deterministic box
// (agent-<skill_id>) WITHOUT running it: no token is minted, nothing is
// seeded, and the model is never called (#2272). RunAgentSkill/RunCrew are
// otherwise the ONLY path that creates this box, and both require an
// inference credential just to get that far — so a human could never get a
// box provisioned ahead of time to sign in to its coding agent. This RPC is
// the create-or-reuse step on its own, same gate as RunAgentSkill
// (agents:run) since it still creates/starts a container under the tenant.
func (s *AgentSkillServer) ProvisionSkillBox(ctx context.Context, req *pb.ProvisionSkillBoxRequest) (*pb.ProvisionSkillBoxResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRun); err != nil {
		return nil, err
	}
	if req.GetSkillId() == "" {
		return nil, status.Error(codes.InvalidArgument, "skill_id is required")
	}
	skill, err := s.catalog.Get(req.GetSkillId())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	_, box, fresh, err := s.provisionBoxOnly(ctx, skill, req.GetBackendId(), req.GetPool())
	if err != nil {
		return nil, err
	}
	return &pb.ProvisionSkillBoxResponse{Container: box, FreshlyProvisioned: fresh}, nil
}

// codeEngineFor maps a skill's AgentEngine to the internal/coderun/engine
// Name whose CredentialStatusScript probes it. AGENT_ENGINE_UNSPECIFIED
// resolves to Claude — the only engine the "coding-agent" recipe installs
// today. AGENT_ENGINE_CODEX/GEMINI have no probe yet (#2273 adds Codex's);
// ok=false rather than a guess.
func codeEngineFor(e pb.AgentEngine) (name engine.Name, resolved pb.AgentEngine, ok bool) {
	switch e {
	case pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED, pb.AgentEngine_AGENT_ENGINE_CLAUDE:
		return engine.NameClaude, pb.AgentEngine_AGENT_ENGINE_CLAUDE, true
	default:
		return "", e, false
	}
}

// codeCredentialSourcePB converts the engine package's credential-status
// answer to the wire enum. Never reached with an unrecognized value: the
// caller already validated src via engine.ParseCredentialStatusSource.
func codeCredentialSourcePB(src engine.CredentialStatusSource) pb.CodeCredentialSource {
	switch src {
	case engine.CredentialStatusInteractive:
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_INTERACTIVE
	case engine.CredentialStatusAPIKey:
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_API_KEY
	case engine.CredentialStatusNone:
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_NONE
	case engine.CredentialStatusExpired:
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_EXPIRED
	default:
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_UNSPECIFIED
	}
}

// GetSkillBoxCredentialStatus reports whether a skill's already-provisioned
// box has a credential its configured coding engine would use to sign in —
// NAMES ONLY, never a value (#2272, mirroring #2030's `code install` verify
// posture). Every check the rendered probe runs is a file's existence or an
// env var's name; this method never reads, logs, or transmits a credential
// at any layer, box to daemon to caller. Read-only: agents:read, the same
// gate ListAgentEngines uses for its own readiness report.
func (s *AgentSkillServer) GetSkillBoxCredentialStatus(ctx context.Context, req *pb.GetSkillBoxCredentialStatusRequest) (*pb.GetSkillBoxCredentialStatusResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRead); err != nil {
		return nil, err
	}
	if req.GetSkillId() == "" {
		return nil, status.Error(codes.InvalidArgument, "skill_id is required")
	}
	skill, err := s.catalog.Get(req.GetSkillId())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	name := "agent-" + skill.Id
	if err := auth.AuthorizeTenant(ctx, name); err != nil {
		return nil, err
	}
	if s.recipes == nil || s.recipes.containers == nil || s.recipes.containers.manager == nil {
		return nil, status.Error(codes.FailedPrecondition, "no container manager configured on this daemon")
	}
	if info, gerr := s.recipes.containers.manager.Get(name); gerr != nil || info == nil {
		return nil, status.Errorf(codes.NotFound,
			"box %s is not provisioned yet; call ProvisionSkillBox (or run the skill) first", name)
	}

	engineName, resolvedEngine, ok := codeEngineFor(skill.GetEngine())
	if !ok {
		return nil, status.Errorf(codes.Unimplemented,
			"credential-status check for engine %s is not implemented yet", agentengine.EnvValue(skill.GetEngine()))
	}
	src, err := s.probeBoxCredential(name+"-container", engineName, resolvedEngine)
	if err != nil {
		return nil, err
	}

	return &pb.GetSkillBoxCredentialStatusResponse{
		Engine:           resolvedEngine,
		CredentialSource: src,
		CheckedAt:        timestamppb.Now(),
	}, nil
}

// probeBoxCredential runs engineName's credential-status probe in
// containerName and returns the raw wire source for the manual RPC. The
// watcher uses a separate alert-policy script (#2790). Errors are gRPC statuses.
func (s *AgentSkillServer) probeBoxCredential(containerName string, engineName engine.Name, resolvedEngine pb.AgentEngine) (pb.CodeCredentialSource, error) {
	const unknown = pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_UNSPECIFIED
	script, ok := engine.CredentialStatusScript(engineName)
	if !ok {
		return unknown, status.Errorf(codes.Unimplemented,
			"credential-status check for engine %s is not implemented yet", agentengine.EnvValue(resolvedEngine))
	}
	return s.probeBoxCredentialScript(containerName, script)
}

// probeBoxCredentialScript executes either the raw-status or alert-policy probe.
// Both return source names only; neither forwards credential-file contents.
func (s *AgentSkillServer) probeBoxCredentialScript(containerName, script string) (pb.CodeCredentialSource, error) {
	const unknown = pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_UNSPECIFIED
	stdout, stderr, exitCode, execErr := s.recipes.containers.manager.ExecWithExitCode(containerName, []string{"bash", "-c", script})
	if execErr != nil {
		return unknown, status.Errorf(codes.Internal, "checking credential status on %s: %v", containerName, execErr)
	}
	if exitCode != 0 {
		return unknown, status.Errorf(codes.Internal, "credential-status probe on %s exited %d: %s", containerName, exitCode, strings.TrimSpace(stderr))
	}
	src, perr := engine.ParseCredentialStatusSource(strings.TrimSpace(stdout))
	if perr != nil {
		return unknown, status.Errorf(codes.Internal, "credential-status probe on %s: %v", containerName, perr)
	}
	return codeCredentialSourcePB(src), nil
}

// RunAgentSkill provisions a skill's box, mints a token scoped to exactly the
// skill's allowed_scopes, seeds the prompt/token/input into the box, and
// returns the box. Gated on agents:run; the inner provisioning still enforces
// containers:write + tenant authz via CreateContainer.
//
// Phase 0 limitations (documented seams):
//   - The in-box agent loop is the agent-runtime image's job. A run whose
//     in-box agent fails (or whose box does not ship agent-runtime) returns a
//     gRPC error naming the run, never OK with an empty artifact_json.
//   - The box name is derived deterministically from the skill id, so two
//     concurrent runs of the same skill collide. Per-run boxes / a warm pool
//     are a later concern (see docs/EPHEMERAL-SANDBOX-DESIGN.md).
//   - allowed_peers is inert until Phase 2 (eBPF enforcement).
func (s *AgentSkillServer) RunAgentSkill(ctx context.Context, req *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
	run, err := s.beginSkillRun(ctx, req)
	if err != nil {
		return nil, err
	}
	runID, containerName, box, lease, gitCommit, workspacePath := run.runID, run.containerName, run.box, run.lease, run.gitCommit, run.workspacePath
	// The defer sits AFTER beginSkillRun on purpose: a run that failed to
	// provision has nothing left to end (see beginSkillRun).
	defer s.endRunLease(ctx, lease, s.boxWiper(), runExitReason)

	// Run the in-box agent loop (Phase 4a) and read its artifact back. A
	// failed run is an error, never a success with an empty artifact: the
	// caller cannot otherwise tell "the agent failed" from "the agent had
	// nothing to say". See inBoxRunStatus for the codes.
	//
	// Note on "caller-cancel": runInBoxAgentResult takes no context (it goes
	// through ExecWithExitCode), so cancelling the RPC does NOT interrupt the in-box
	// process — the lease ends when the exec returns, under a detached context.
	// Making the exec cancellable is a separate change and doesn't alter this.
	//
	// #1860: this read happens before the function returns, and `defer`s run
	// after a function's return values are computed but before it returns to
	// its caller — so artifact.json is always read into this Go string BEFORE
	// endRunLease's directory removal ever runs. A run whose artifact was
	// returned never loses it to the wipe.
	artifact, err := s.runInBoxAgentResult(containerName, lease.SeedDir, runID, run.skillID, run.engineRes, run.model)
	if err != nil {
		log.Printf("[agent-skill] run %s: %v", runID, err)
		return nil, inBoxRunStatus(runID, err)
	}
	return &pb.RunAgentSkillResponse{
		Container:     box,
		ArtifactJson:  artifact,
		RunId:         runID,
		GitCommit:     gitCommit,
		WorkspacePath: workspacePath,
	}, nil
}

// startedSkillRun is what beginSkillRun hands back: a provisioned,
// registered run whose lease the caller must end (endRunLease) once the
// in-box agent returns.
type startedSkillRun struct {
	runID, containerName     string
	skillID                  string
	box                      *pb.Container
	lease                    runlease.Lease
	gitCommit, workspacePath string
	// engineRes and model (#2222) are provisionSkillBoxWith's resolved engine
	// and the skill's manifest model, carried through so whichever exec path
	// a caller launches next (runInBoxAgentResult, a dispatcher's own re-exec) uses
	// the SAME engine the box's gateway token was already bound to.
	engineRes agentengine.Resolved
	model     string
}

// beginSkillRun is RunAgentSkill up to (and including) registering the
// run: scope check, run id, tracker_connection validation against the
// caller's tenant, catalog lookup, box provisioning with the run JWT
// minted (bound to tracker_connection), and run registration. Shared by
// RunAgentSkill and the tracker dispatcher's RunStarter (#2022) so a
// dispatched run goes through exactly the same path. On error nothing is
// left to end: provisionSkillBox ends a partially minted lease itself.
func (s *AgentSkillServer) beginSkillRun(ctx context.Context, req *pb.RunAgentSkillRequest) (*startedSkillRun, error) {
	return s.beginSkillRunWith(ctx, req, provisionOptions{})
}

// provisionOptions are the internal knobs a caller of beginSkillRunWith
// can set that the public RunAgentSkill contract does not expose.
type provisionOptions struct {
	instanceID string // internal isolated graph box identity; empty keeps catalog identity
	// gitSourceBestEffort keeps a run going without a workspace when
	// its git_source cannot be fetched. A tracker-dispatched run (#2023)
	// sets it: its git_source is the connection's repository fetched
	// with NO credential (none may enter the box), which fails for a
	// private repository — that run must still read the issue and post
	// its result comment; only the doc change (SubmitTrackerChange,
	// which needs a recorded git_commit) is unavailable to it.
	gitSourceBestEffort bool
	// engineOverride (#2228) wins over the skill's own manifest `engine` when
	// set — see agentengine.Override. AGENT_ENGINE_UNSPECIFIED (the zero
	// value) means "no override, use the manifest", unchanged pre-#2228
	// behavior for every caller that doesn't set this.
	engineOverride pb.AgentEngine
	// orgID is the validated on_behalf_of_org_id of the request: the
	// org whose provider key the run spends. Empty = derive the owner from the
	// box label or the caller, as before.
	orgID string
}

// beginSkillRunWith is beginSkillRun with internal provisioning options.
func (s *AgentSkillServer) beginSkillRunWith(ctx context.Context, req *pb.RunAgentSkillRequest, opts provisionOptions) (*startedSkillRun, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRun); err != nil {
		return nil, err
	}
	if req.SkillId == "" {
		return nil, status.Error(codes.InvalidArgument, "skill_id is required")
	}

	// Resolve the run id before any box work: it is bound into every credential
	// this run is given, so a malformed one must fail the RPC, not the run.
	runID, err := resolveRunID(req.GetRunId())
	if err != nil {
		return nil, err
	}

	// Validate tracker_connection against the CALLER's own tenant before any
	// box work, same reasoning as the run id above: a request naming a
	// connection it doesn't own must fail the RPC, not mint a claim that
	// looks legitimate. Empty means the run isn't bound to a connection —
	// unchanged, pre-#1922 behavior (#1922 step 6, design note decision D3).
	if err := s.validateTrackerConnection(ctx, req.GetTrackerConnection()); err != nil {
		return nil, err
	}

	skill, err := s.catalog.Get(req.SkillId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	// #2228: a per-invocation override on the request wins over the
	// manifest's own `engine` field. opts is a local copy (Go passes structs
	// by value), so this only affects this call — a caller's own
	// gitSourceBestEffort (set above us) is preserved alongside it.
	opts.engineOverride = req.GetEngine()
	// #3264: a per-run model wins over the skill's own, on a copy so the shared
	// catalog skill is never mutated; refused before any box work.
	if skill, err = applyRunModel(skill, req); err != nil {
		return nil, err
	}
	if opts.orgID, err = authorizeRunOrg(ctx, req.GetOnBehalfOfOrgId()); err != nil {
		return nil, err
	}
	instance := skill
	if opts.instanceID != "" {
		instance = proto.Clone(skill).(*pb.AgentSkill)
		instance.Id = opts.instanceID
	}
	containerName, box, lease, gitCommit, workspacePath, engineRes, err := s.provisionSkillBoxWith(ctx, instance, req.BackendId, req.Pool, req.InputJson, runID,
		req.GetGitSource(), req.GetGitRef(), req.GetGitCredential(), req.GetTrackerConnection(), opts)
	if err != nil {
		return nil, err
	}

	// Durable run->skill record (#2122), so TailRunLog can still resolve this
	// run's journal after a daemon restart, the same way a crew run already
	// does from its own record. Only a standalone skill run needs this: a
	// crew member is provisioned through provisionSkillBox directly (not
	// through this function), and the crew's own record already covers it.
	// Best-effort — a write failure here degrades to pre-#2122 behavior (the
	// in-memory runIndex still resolves the run until this process exits)
	// rather than failing a run that otherwise provisioned successfully.
	if s.recordSkillRun != nil {
		if err := s.recordSkillRun(ctx, runID, skill.Id); err != nil {
			log.Printf("[agent-skill] run %s: record durable run index: %v", runID, err)
		}
	}

	// The run holds its credentials for exactly as long as the run (#1817). A
	// provisioning failure has nothing to end but a partially minted lease,
	// which provisionSkillBox ends itself before returning its error — which
	// is why the callers' `defer endRunLease` (RunAgentSkill, and
	// finishDispatchedRun for a dispatched run) only exists once this
	// function has returned a run. From there every exit path — the artifact,
	// an agent error, a cancelled caller — revokes both jtis and wipes the
	// seed files.
	//
	// RESOLVED (#1860) — the box is still shared by skill id ("agent-"+skill.Id,
	// see provisionSkillBox), but the seed directory no longer is: every run,
	// crew member, and queue worker gets its own seedDirFor(runID), so this
	// run's exit removes only ITS OWN directory and never touches a co-resident
	// crew member's or worker's files. What remains open, by design and matching
	// #1860's own "Not in scope": a leftover in-box PROCESS (not a file) from an
	// earlier run can still read a later run's live token out of /proc while
	// both happen to share the box's UID — closing that needs a per-run box or a
	// Clean()-style process reset, not a seed-path change. Crew/queue leases are
	// also still never explicitly ended here (they outlive this RPC by design);
	// see docs/architecture/execution-scoped-authorization.md §3.
	// Record this run in the in-memory registry (#1922) so other daemon
	// components — the tracker broker's ClaimTrackerIssue liveness check
	// and identity stamp — can resolve run_id -> skill/model without a
	// database round trip. Box/GitCommit/Workspace/GitRef (#1923) let
	// SubmitTrackerChange resolve the same run to what it needs to
	// bundle and to open a change request against without a second
	// lookup path; GitCommit/Workspace are empty exactly when this run
	// had no git_source, which SubmitTrackerChange must treat as
	// FAILED_PRECONDITION. Symmetric with endRunLease's Unregister
	// below: a run is "live" for exactly the window between successful
	// provisioning (here) and its lease ending.
	if s.runs != nil {
		s.runs.Register(runID, runlease.Info{
			SkillID:   skill.Id,
			Model:     skill.Model,
			Engine:    engineRes.Engine,
			Box:       containerName,
			GitCommit: gitCommit,
			Workspace: workspacePath,
			GitRef:    req.GetGitRef(),
		})
	}
	return &startedSkillRun{
		runID: runID, containerName: containerName, skillID: skill.Id, box: box, lease: lease,
		gitCommit: gitCommit, workspacePath: workspacePath, engineRes: engineRes, model: skill.GetModel(),
	}, nil
}

// boxWiper is the seam runlease.End wipes a run's seed files through.
// *container.Manager already satisfies runlease.Wiper; nil when no container
// manager is wired (then the wipe is skipped and reported as not done),
// mirroring runInBoxAgentResult's own guard. The boxOps override, when set, wipes
// too, so a test sees the same box the run was seeded into.
func (s *AgentSkillServer) boxWiper() runlease.Wiper {
	if s.boxOps != nil {
		return s.boxOps
	}
	if s.recipes == nil || s.recipes.containers == nil || s.recipes.containers.manager == nil {
		return nil
	}
	return s.recipes.containers.manager
}

// endRunLease revokes a run's credentials and wipes its seed files, then
// records what happened.
//
// The context is detached from the caller's (context.WithoutCancel) so a
// cancelled or timed-out RPC still gets its credentials killed — the whole
// point of the lease. Every step that can block is bounded: the revokes by
// endRunLeaseCeiling, the wipe by runlease's own timer, and the audit write by
// auditWriteBudget inside auditRunLease. The wiper is passed in rather than
// read off s so the behavior is testable without a live container backend.
func (s *AgentSkillServer) endRunLease(ctx context.Context, lease runlease.Lease, w runlease.Wiper, reason string) {
	if len(lease.Credentials) == 0 && lease.RunID == "" {
		return // nothing was ever issued
	}

	if s.runs != nil {
		s.runs.Unregister(lease.RunID)
	}

	detached := context.WithoutCancel(ctx)
	lctx, cancel := context.WithTimeout(detached, endRunLeaseCeiling)
	out := runlease.End(lctx, lease, s.revocations, w, reason)
	cancel()

	if len(out.Unrevoked) > 0 {
		// One line per run, naming the run, so an operator running without
		// Postgres can see exactly which credentials are now alive until their
		// 30-minute expiry.
		log.Printf("[agent-skill] run %s: %d credential(s) could not be revoked before expiry (no revocation store, or the store rejected them)",
			lease.RunID, len(out.Unrevoked))
	}
	for _, err := range out.Errs {
		log.Printf("[agent-skill] run %s: ending lease: %v", lease.RunID, err)
	}

	// After the revocations: with its credentials dead the run cannot
	// start another create, so its leftover reservations protect nothing.
	s.releaseLineageReservations(detached, lease.RunID)

	s.auditRunLease(detached, "agent.run_lease_end", lease.RunID, runLeaseEndPayload(lease, reason, out))
}

// lineageReleaseBudget bounds the reservation sweep in endRunLease, for the
// same reason auditWriteBudget bounds the audit write: it runs on a
// detached context inside the RPC's deferred cleanup.
const lineageReleaseBudget = 3 * time.Second

// releaseLineageReservations sweeps the fan-out reservations runID still
// holds (#2062): ones RecordChild could not clear, which would otherwise
// count against the run's max_children_per_run forever. Scoped to the
// run's own tenant — the authenticated subject the run was started for,
// the same identity its token was minted for and auditRunLease records —
// because a run id alone can be caller-chosen. With no subject nothing is
// swept: over-counting is the fail-closed side. Best-effort: a failure is
// logged and leaves the rows, which ListTrackerDispatches still shows.
func (s *AgentSkillServer) releaseLineageReservations(ctx context.Context, runID string) {
	if s.lineageReservations == nil || runID == "" {
		return
	}
	username, _, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok || username == "" {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, lineageReleaseBudget)
	defer cancel()
	n, err := s.lineageReservations.ReleaseRunReservations(rctx, username, runID)
	if err != nil {
		log.Printf("[agent-skill] run %s: releasing lineage reservations: %v", runID, err)
		return
	}
	if n > 0 {
		log.Printf("[agent-skill] run %s: released %d stale lineage reservation(s) at lease end", runID, n)
	}
}

// agentRuntimeReleaseTag returns the GitHub release tag the agent-runtime box
// pulls its artifacts from. version.GetVersion() is the BARE semver ("0.26.6")
// — the release workflow builds with VERSION=${tag#v}, so the `v` is stripped
// at build time — but the git tag and release are `v`-prefixed ("v0.26.6"),
// and the recipe's post_start uses this value directly in
// raw.githubusercontent.com/<repo>/<ref>/... and releases/download/<ref>/...
// URLs. Without the `v` those 404 and the best-effort assembly silently skips,
// so the box comes up without the in-box loop. Re-add the prefix (idempotent —
// a value that already carries it, or a dev/unpublished version, is left as-is
// and just degrades to skip-assembly as before).
func agentRuntimeReleaseTag() string {
	v := version.GetVersion()
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// provisionSkillBox provisions a skill's box and gets it ready to run: resolve
// the box recipe, deploy it, mint a JWT scoped to exactly the skill's
// allowed_scopes, seed the prompt/token/input/card, and compile allowed_peers
// into the per-box egress policy. It does NOT run the loop — RunAgentSkill runs
// it one-shot, RunCrew starts it in serve mode. Returns the container name +
// the provisioned Container.
// mintedAgentAct computes the RFC 8693 `act` delegation claim (#1677) for a
// token minted on behalf of the caller authenticated in ctx — used by both
// RunAgentSkill (single skill) and RunCrew (per-member boxes, both funnel
// through provisionSkillBox) via GenerateDelegatedToken.
//
// Derived ONLY from ctx — never from the request proto — which is the
// anti-forgery invariant this claim exists to hold: RunAgentSkillRequest
// and RunCrewRequest have no actor-ish field a caller could set, and this
// function doesn't accept the request as a parameter at all, so there is
// nothing for a caller to forge through.
//
// Nests rather than overwrites: if the caller's OWN token already carried
// an act (it is itself a derived/agent token — e.g. an agent box that
// itself holds agents:run calling RunAgentSkill again), the new token's act
// wraps {caller's subject, caller's own act}, so the chain always resolves
// back to the root human principal at whatever depth it's read.
// validateTrackerConnection checks that connName, when non-empty, names a
// tracker connection owned by the CALLER's own tenant (never a request
// field's claimed identity) — the anti-forgery check that makes minting it
// into the run's `tracker_conn` claim trustworthy. Empty connName is valid
// (no binding requested) and always passes without touching the store.
func (s *AgentSkillServer) validateTrackerConnection(ctx context.Context, connName string) error {
	if connName == "" {
		return nil
	}
	if s.trackerConnections == nil {
		return status.Error(codes.FailedPrecondition, "tracker connections not configured on this daemon")
	}
	username, _, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok || username == "" {
		return status.Error(codes.Unauthenticated, "no authenticated subject")
	}
	if _, err := s.trackerConnections.Get(ctx, username, connName); err != nil {
		if errors.Is(err, tracker.ErrNotFound) {
			return status.Errorf(codes.InvalidArgument, "tracker_connection %q not found for tenant %q", connName, username)
		}
		return status.Errorf(codes.Internal, "check tracker_connection: %v", err)
	}
	return nil
}

func mintedAgentAct(ctx context.Context) *auth.Actor {
	username, _, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok || username == "" {
		return nil // unauthenticated/system context — nothing meaningful to record
	}
	callerAct, _ := auth.ActFromGRPCContext(ctx)
	return &auth.Actor{Subject: username, Act: callerAct}
}

// mintedRunTenant is the tenant a run token is minted for (#2268): the
// caller's own verified subject — the identity requireDispatchCaller
// demanded and validateTrackerConnection looked the connection up under —
// so the run_tenant claim can only ever name a tenant whose own token
// started the run. Like mintedAgentAct, derived ONLY from ctx, never from
// the request proto. "" (no claim) for an unauthenticated/system context.
func mintedRunTenant(ctx context.Context) string {
	username, _, ok := auth.SubjectFromGRPCContext(ctx)
	if !ok {
		return ""
	}
	return username
}

// mintedAgentTokenScopes computes the scopes for a skill's in-box token: the
// intersection of the caller's own granted scopes and the skill manifest's
// allowed_scopes (#1676). auth.ScopesFromGRPCContext — not the plain
// ScopesFromContext — is required here: the primary API surface is REST via
// grpc-gateway, and the HTTP middleware propagates the caller's scopes claim
// through outgoing gRPC metadata (internal/auth/middleware.go), not a context
// value that would survive the HTTP→gateway→gRPC hop. RequireScope and
// AuthorizeTenant already read the caller this same way.
//
// runForbiddenScopes are then stripped unconditionally — caught in review
// of #1924 (CWE-862): tracker:admin gates tracker connection CRUD
// (repointing the broker at a different project or credential), an
// operator decision that must never reach a bounded skill run no matter
// how permissive the dispatching caller or the skill manifest's own
// allowed_scopes are. Without this, a caller with the admin role calling
// RunAgentSkill on a manifest that (mistakenly or not) lists tracker:admin
// in allowed_scopes would mint a run token that could redirect the
// broker's held credential — the exact thing a run-scoped token binding
// to one connection (#1922 step 6) exists to prevent.
func mintedAgentTokenScopes(ctx context.Context, skill *pb.AgentSkill) []string {
	callerScopes, _ := auth.ScopesFromGRPCContext(ctx)
	granted := auth.IntersectScopes(callerScopes, skill.AllowedScopes)
	return auth.ExcludeScopes(granted, runForbiddenScopes...)
}

// runForbiddenScopes never reach a minted run token, regardless of what
// the dispatching caller or the skill manifest's allowed_scopes grant.
// A future admin-tier scope gets added here on the same reasoning, not by
// auditing every skill manifest for it.
//
//   - tracker:admin — see mintedAgentTokenScopes's doc comment.
//   - tokens:delegate (#2069) — ExchangeDelegatedToken mints a token with
//     no run_id, and every run-token guard (#2060's lineage binding,
//     #2112's taskRunID, the run_log claim check) applies only
//     `if runID != ""`. A run token holding it could delegate its way out
//     of its own run and shed all of them at once. No shipped skill grants
//     it; a custom skill via CONTAINARIUM_SKILLS_DIR could. The handler
//     also refuses any run-bound caller outright, so a run token minted
//     before this entry existed is covered too.
//   - code:answer (#3191) — answers a box run's permission requests. The
//     run token lives on the box with the agent, so holding it would let
//     the agent approve its own tool calls.
var runForbiddenScopes = []string{auth.ScopeTrackerAdmin, auth.ScopeTokensDelegate, auth.ScopeCodeAnswer}

// mintRunToken mints the run's platform JWT — the one credential every
// in-box call back to the daemon presents — and returns it with the lease
// credential record (jti + expiry) the run's exit revokes.
//
// The token is minted for the box's own subject (agent-<skill-id>, no
// roles), scoped to the intersection of the CALLER's own granted scopes
// and the skill's allowed_scopes (#1676: the manifest is a ceiling, never
// a floor — a caller with no scopes claim/wildcard gets the manifest
// unchanged, anyone else only receives scopes they already hold), carrying
// the dispatching caller as its `act` delegation claim (#1677) so an
// auditor asking "who authorized this?" doesn't get the name of a robot.
// Minted through the WithRun variant so the daemon keeps the jti + expiry
// of what it issued, with runID so the token itself says which run it
// belongs to (the `run_id` claim), and with trackerConnection — already
// validated against the caller's own tenant by validateTrackerConnection
// before this function was called — so the token also says which tracker
// connection the run is bound to (the `tracker_conn` claim, #1922 step 6).
// Empty trackerConnection mints no claim, unchanged pre-#1922 behavior.
//
// The token also carries the tenant the run was started for (the
// `run_tenant` claim, #2268) — the caller's own verified subject, the same
// identity validateTrackerConnection checked the connection under — so the
// tracker verbs can authorize the run for THAT tenant: its own subject is
// the box, which auth.AuthorizeTenant can never match to the tenant the
// in-box tracker_* tools name. See auth.AuthorizeTrackerTenant.
//
// This is the ONLY place a run token is minted: provisionSkillBoxWith
// calls it for every run (RunAgentSkill, RunCrew, the tracker dispatcher),
// and the tests that prove what a real run token can and cannot do call
// it directly (#2268) rather than hand-building a subject.
func (s *AgentSkillServer) mintRunToken(ctx context.Context, skill *pb.AgentSkill, runID, trackerConnection string) (token string, cred runlease.Credential, err error) {
	name := agentBoxPrefix + skill.Id
	run := auth.RunBinding{RunID: runID, TrackerConn: trackerConnection, Tenant: mintedRunTenant(ctx)}
	token, minted, mintErr := s.tokens.GenerateDelegatedTokenWithRun(name, []string{}, agentTokenTTL, mintedAgentAct(ctx), run, mintedAgentTokenScopes(ctx, skill)...)
	if mintErr != nil {
		return "", runlease.Credential{}, status.Errorf(codes.Internal, "failed to mint scoped agent token: %v", mintErr)
	}
	return token, runlease.Credential{
		Kind: runlease.KindPlatformJWT, JTI: minted.JTI, ExpiresAt: minted.ExpiresAt,
	}, nil
}

// provisionSkillBox provisions (or reuses) the skill's box, mints its
// credentials, seeds the task, and — when gitSource is set (#1859) —
// shallow-fetches that repo into the box's workspace before the agent starts.
// The fetch runs AFTER the seed exec succeeds and BEFORE the allowed_peers
// policy is applied: seeding first means a failed fetch still leaves a
// consistent, end-able lease behind (same reasoning as the seed-failure path
// below); applying policy last means a fetch that needs the platform egress
// allowlist (git-installer package fetches) isn't fighting the skill's own
// restrictive policy while it runs.
//
// The workspace is box-level (not yet per-run — see #1860), so concurrent
// runs of the same skill overwrite each other's checkout exactly as they
// already overwrite each other's seed files; #1860 gives both their own
// per-run directory.
// The 7th return value, engineRes, is the engine this run actually resolved
// to (#2222, agentengine.Resolve) — the daemon's default when the skill names
// none, or the skill's own named engine, refused up front in gateway mode if
// its provider has no resolvable key. Callers thread it into whichever exec
// path launches the box (run/serve/poll) so the box gets the SAME engine
// readiness reported.
func (s *AgentSkillServer) provisionSkillBox(ctx context.Context, skill *pb.AgentSkill, backendID, pool, inputJSON, runID, gitSource, gitRef, gitCredential, trackerConnection string) (containerName string, box *pb.Container, lease runlease.Lease, gitCommit, workspacePath string, engineRes agentengine.Resolved, err error) {
	return s.provisionSkillBoxWith(ctx, skill, backendID, pool, inputJSON, runID, gitSource, gitRef, gitCredential, trackerConnection, provisionOptions{})
}

// provisionSkillBoxWith is provisionSkillBox with internal options.
// provisionBoxOnly resolves a skill's deterministic box name
// (agent-<skill-id>) and creates it if absent, or starts-and-reuses it if
// present — WITHOUT minting a run token, resolving an engine, seeding
// anything, or fetching git. This is the create-or-reuse half of
// provisionSkillBoxWith (everything up to its old `containerName = name +
// "-container"` line), factored out for #2272: ProvisionSkillBox calls this
// directly so a crew member's box can exist — and a human can sign in to its
// coding agent — before any inference credential does. No call on this path
// ever reaches a model.
func (s *AgentSkillServer) provisionBoxOnly(ctx context.Context, skill *pb.AgentSkill, backendID, pool string) (containerName string, box *pb.Container, freshBox bool, err error) {
	// Phase 0 supports only the recipe_id box form (catalog skills). Inline
	// recipes are an API-only construct deferred to a later phase.
	recipeID := skill.GetRecipeId()
	if recipeID == "" {
		return "", nil, false, status.Error(codes.Unimplemented,
			"inline-recipe skills are not supported yet; use a skill that references a recipe_id")
	}

	// Deterministic box identity (concurrent same-skill runs collide — a later
	// per-run-box / warm-pool concern, see docs/EPHEMERAL-SANDBOX-DESIGN.md).
	name := "agent-" + skill.Id
	if err := auth.AuthorizeTenant(ctx, name); err != nil {
		return "", nil, false, err
	}

	// Provision the box, idempotently. The normal skill flow is run → (set a
	// secret / inspect) → run again, and a crew re-drives its members every
	// run, so the SAME box name recurs. The deploy path's CreateContainer
	// errors on an existing instance ("already exists"), which would make every
	// re-run fail. So if the box is already provisioned, reuse it: skip deploy
	// (and its one-time post_start assembly) and just re-mint the token,
	// re-seed, and re-apply policy below. A stopped box (idle-sleep, host
	// reboot) is started so the subsequent seed-exec / loop-exec lands.
	if info, gerr := s.recipes.containers.manager.Get(name); gerr == nil && info != nil {
		if info.State != "Running" {
