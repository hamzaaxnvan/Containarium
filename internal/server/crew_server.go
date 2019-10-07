package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/pkg/core/crews"
	"github.com/footprintai/containarium/pkg/core/skills"
	"github.com/footprintai/containarium/pkg/core/taskgraph"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CrewServer implements the gRPC CrewService (Phase 3). A crew is a
// collaborating set of skills bound to a task purpose. RunCrew validates the
// crew's topology against the union of members' allowed_peers (rejecting any
// edge the Phase 2 trust fabric would drop), provisions each member's box by
// reusing the AgentSkillService, and threads one trace_id through the run.
//
// Phase 3 seam: the actual inter-agent choreography and artifact aggregation
// are the in-box agent loop's job (the agent-runtime image, not yet wired), so
// a run lands in RUNNING once the boxes are up + network-gated, not COMPLETED.
type CrewServer struct {
	pb.UnimplementedCrewServiceServer
	catalog *crews.Manager
	skills  *skills.Manager
	agents  *AgentSkillServer // reuse RunAgentSkill: provision + scoped token + per-box net policy
	runs    CrewRunStore
	// owner identifies this daemon on the runs it drives, so startup
	// reconciliation can tell its own stranded runs from a peer's live ones
	// (#1322). Empty on a daemon with no backend id configured, which reads
	// as unowned — the single-daemon case, where that is correct.
	owner string
}

// SetOwner records which daemon this server drives runs as.
func (s *CrewServer) SetOwner(id string) { s.owner = id }

// NewCrewServer wires the crew service to the agent-skill server (for box
// provisioning) and the embedded crew + skill catalogs.
func NewCrewServer(agents *AgentSkillServer) *CrewServer {
	s := &CrewServer{
		catalog: crews.GetDefault(),
		skills:  skills.GetDefault(),
		agents:  agents,
		runs:    NewMemCrewRunStore(),
	}
	// TailRunLog (#2096) resolves a crew run's members from its run record.
	// #2122 reuses the same store + hook for a standalone skill run: recordSkillRun
	// writes it, runMembers below resolves it, reapSkillRun sweeps it.
	if agents != nil {
		agents.crewRunMembers = s.runMembers
		agents.recordSkillRun = s.recordSkillRun
		agents.reapSkillRun = s.reapSkillRunRecord
	}
	return s
}

// runMembers resolves a run id to its member skill ids, entry first. Two
// shapes share this store (#2122): a crew run (crew_id set) resolves its
// members from the crew catalog, exactly as before; a standalone skill run
// (crew_id empty, skill_id set — recordSkillRun's record) has no crew to
// look up and IS its own single-member list.
func (s *CrewServer) runMembers(ctx context.Context, runID string) ([]string, bool, error) {
	run, ok, err := s.runs.Get(ctx, runID)
	if err != nil || !ok {
		return nil, false, err
	}
	if run.GetCrewId() == "" {
		if run.GetSkillId() == "" {
			return nil, false, nil
		}
		return []string{run.GetSkillId()}, true, nil
	}
	crew, err := s.catalog.Get(run.GetCrewId())
	if err != nil {
		return nil, false, nil
	}
	return append([]string(nil), crew.GetSkillIds()...), true, nil
}

// recordSkillRun persists a standalone skill run's durable run->skill record
// (#2122), reusing the crew-run store rather than a second persistence
// mechanism: crew_id is left empty (this run has no crew) and skill_id is
// set, which runMembers above resolves directly. Wired onto AgentSkillServer
// so beginSkillRunWith can call it right after a skill run is provisioned.
func (s *CrewServer) recordSkillRun(ctx context.Context, runID, skillID string) error {
	return s.runs.Put(ctx, &pb.CrewRun{Id: runID, SkillId: skillID})
}

// reapSkillRunRecord removes a standalone skill run's durable record once its
// journal directory has been reaped (#2122), coupling the record's lifetime
// to --run-journal-retention. Delegates the crew-run guard to the store (see
// CrewRunStore.DeleteSkillRun) — this method is safe to call with ANY reaped
// run id, crew or skill.
func (s *CrewServer) reapSkillRunRecord(ctx context.Context, runID string) error {
	return s.runs.DeleteSkillRun(ctx, runID)
}

// ListCrews returns all built-in crews.
func (s *CrewServer) ListCrews(ctx context.Context, _ *pb.ListCrewsRequest) (*pb.ListCrewsResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeCrewsRead); err != nil {
		return nil, err
	}
	return &pb.ListCrewsResponse{Crews: s.catalog.List()}, nil
}

// GetCrew returns a single crew by ID.
func (s *CrewServer) GetCrew(ctx context.Context, req *pb.GetCrewRequest) (*pb.GetCrewResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeCrewsRead); err != nil {
		return nil, err
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	crew, err := s.catalog.Get(req.Id)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &pb.GetCrewResponse{Crew: crew}, nil
}

// ValidateTaskGraph checks a task graph against this daemon's crew and skill
// catalogs (#2803) — the same taskgraph.Validate `containarium crew plan
// validate` runs locally against the embedded catalogs.
func (s *CrewServer) ValidateTaskGraph(ctx context.Context, req *pb.ValidateTaskGraphRequest) (*pb.ValidateTaskGraphResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeCrewsRead); err != nil {
		return nil, err
	}
	res, err := taskgraph.Validate(req.GetGraph(), s.skills, s.catalog)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.ValidateTaskGraphResponse{Order: res.Order, Depth: uint32(res.Depth)}, nil // #nosec G115 -- Depth <= taskgraph.MaxDepth
}

// RunCrew validates a crew's topology, provisions each member's box under one
// trace_id, and records the run. Gated on crews:run; box provisioning reuses
// RunAgentSkill, so the caller also needs agents:run + containers:write.
func (s *CrewServer) RunCrew(ctx context.Context, req *pb.RunCrewRequest) (*pb.RunCrewResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeCrewsRun); err != nil {
		return nil, err
	}
	if req.CrewId == "" {
		return nil, status.Error(codes.InvalidArgument, "crew_id is required")
	}
	// Resolve the run id before any catalog/topology work, same as
	// RunAgentSkill (#1899): a malformed caller-supplied id must fail the RPC,
	// not the run, and a caller with its own run-tracking id (e.g. Containarium
	// Cloud's CrewRunID) needs it to become this run's id so OSS's audit trail
	// and every member's lease resolve back to the same identifier.
	runID, err := resolveRunID(req.GetRunId())
	if err != nil {
		return nil, err
	}
	// on_behalf_of_org_id: refuse an unauthorized or malformed org
	// attribution before any member is provisioned.
	if _, err := authorizeRunOrg(ctx, req.GetOnBehalfOfOrgId()); err != nil {
		return nil, err
	}
	crew, err := s.catalog.Get(req.CrewId)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	// Before anything is provisioned: a model override that cannot take effect
	// fails the RPC, not the run. A graph run's scope is the skills its nodes use.
	members, scope := crew.GetSkillIds(), fmt.Sprintf("crew %q", crew.GetId())
	if req.GetGraph() != nil {
		members, scope = graphModelMembers(req.GetGraph(), s.catalog), "this graph"
	}
	if err := validateModelOverrides(scope, members, req, s.skillByID); err != nil {
		return nil, err
	}

	if req.GetGraph() != nil {
		return s.runCrewGraph(ctx, req, crew, runID, s.executeGraphNode)
	}

	// Keystone: a crew's wiring may not ask for an A2A hop the trust fabric
	// would drop. Reject before provisioning anything.
	if err := validateCrewTopology(crew, s.skillByID); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	// The three collaborators the run itself is driven through. Injected
	// rather than reached for off s, for the same reason driveCrew already
	// takes its taskSender as a parameter: none of them is reachable from a
	// unit test. provisionSkillBox's seed step can only ever FAIL against a
	// fake container backend ((*container.Manager).Exec type-asserts its
	// backend to the concrete *incus.Client) and SendAgentTask makes a real
	// A2A HTTP call, so without this seam RunCrew's success and
	// driveCrew-failure terminal paths — the ones that must end every member's
	// lease (#2100) — have no unit-testable form at all. The wiper is a
	// parameter for exactly the reason endRunLease already takes one.
	return s.runCrew(ctx, req, crew, runID, crewRunDeps{
		provision:         s.provisionMemberBox,
		send:              s.agents.SendAgentTask,
		wiper:             s.agents.boxWiper(),
		provisionProducer: s.provisionProducerBox,
		producePack:       s.producePackIn,
	})
}

// provisionMemberFunc provisions one crew member's box and starts it serving,
// handing back the lease whose credentials RunCrew must end once the run
// reaches a terminal state. provisionMemberBox in production.
type provisionMemberFunc func(ctx context.Context, skill *pb.AgentSkill, req *pb.RunCrewRequest, runID string) (lease runlease.Lease, gitCommit string, err error)

// crewRunDeps are the collaborators runCrew drives a crew run through. See
// RunCrew's construction of it for why they are parameters.
type crewRunDeps struct {
	provision provisionMemberFunc
	send      taskSender
	wiper     runlease.Wiper
	// provisionProducer and producePack serve a crew's PACK_PRODUCER member
	// (#2799): its box is provisioned with no credentials, and the pack is
	// generated in it by the daemon. s.provisionProducerBox and
	// s.producePackIn in production.
	provisionProducer provisionMemberFunc
	producePack       producePackFunc
}

// producePackFunc writes a crew's final files (artifactJSON) into repoDir in
// box and runs the repository's test commands there, returning the pack.
type producePackFunc func(ctx context.Context, box, repoDir, artifactJSON string) (*pb.ProofPack, error)

// provisionMemberBox is the production provisionMemberFunc: provision the
// member's box (scoped token + per-box allowed_peers policy) and start it in
// serve mode so it serves /tasks for the hops runCrew drives. Members are
// seeded with no task input — the crew delivers per-hop input over A2A.
//
// git_source/git_ref/git_credential (cloud#1554): every member fetches the
// SAME repo+ref into its own per-run workspace — its own box, so trivially
// isolated from every other member's checkout. This is the "git at a pinned
// SHA, not a shared filesystem" design decision (Containarium-cloud
// docs/product/coding-skill-on-a-repo.md §Decided, 2026-09-15): a role
// hand-off between members is a commit on a branch, so two members never
// write the same file at once.
func (s *CrewServer) provisionMemberBox(ctx context.Context, skill *pb.AgentSkill, req *pb.RunCrewRequest, runID string) (runlease.Lease, string, error) {
	// #2228: a per-member override in the request (keyed by skill_id) wins
	// over that member's own manifest `engine` field — same mechanism as
	// RunAgentSkillRequest.engine, applied once per member. A member with no
	// entry gets the map's zero value, AGENT_ENGINE_UNSPECIFIED, which
	// agentengine.Override treats as "no override".
	orgID, err := authorizeRunOrg(ctx, req.GetOnBehalfOfOrgId())
	if err != nil {
		return runlease.Lease{}, "", err
	}
	opts := provisionOptions{engineOverride: req.GetEngineOverrides()[skill.Id], orgID: orgID}
	containerName, _, lease, gitCommit, _, engineRes, err := s.agents.provisionSkillBoxWith(ctx, skill, req.BackendId, req.Pool, "", runID,
		req.GetGitSource(), req.GetGitRef(), req.GetGitCredential(), "", opts)
	if err != nil {
		return runlease.Lease{}, "", err
	}
	// #2222: each member resolves its OWN engine from its OWN manifest (or
	// this request's override, #2228) — a crew whose members name two
	// different engines gets two members on two engines, each with its own
	// provider-bound gateway token.
	if err := s.serveMember(ctx, skill, containerName, lease, engineRes, s.agents.boxWiper()); err != nil {
		return runlease.Lease{}, "", err
	}
	return lease, gitCommit, nil
}

// serveMember starts a freshly provisioned member serving and waits for it to
// answer (#2936). On a failure it ends the lease it was just handed: runCrew
// only ends the leases of members that provisioned successfully ("the member
// that just failed has no lease to end"), so without this a member whose
// runtime never came up would keep a live gateway token and a checkout of the
// caller's repo. The error names the member and its box and wraps
// errInBoxRuntimeMissing when the box image does not ship the runtime, which
// runCrew reports as FailedPrecondition; any other failure is Internal.
func (s *CrewServer) serveMember(ctx context.Context, skill *pb.AgentSkill, containerName string, lease runlease.Lease, engineRes agentengine.Resolved, w runlease.Wiper) error {
	err := s.agents.startServeMode(ctx, containerName, lease.SeedDir, skill.Id, engineRes, skill.GetModel())
	if err == nil {
		return nil
	}
	s.agents.endRunLease(ctx, lease, w, provisionFailedReason)
	return fmt.Errorf("crew member %q (box %s) did not start serving: %w", skill.Id, containerName, err)
}

// endMemberLeases ends every collected member lease: revoke its credentials,
// wipe its seed files, remove its seed dir + fetched workspace, and write the
// agent.run_lease_end audit row — RunAgentSkill's single
// `defer s.endRunLease(...)` applied to a crew's N members (#2100).
//
// Nothing detaches the context here: endRunLease already runs on
// context.WithoutCancel, so a cancelled or timed-out RunCrew still gets every
// member's credentials killed. Best-effort and independent per member —
// endRunLease reports through logs and the audit row rather than an error, so
// one member's unreachable box can never leave the next member's credentials
// alive.
func (s *CrewServer) endMemberLeases(ctx context.Context, leases []runlease.Lease, w runlease.Wiper, reason string) {
	for _, lease := range leases {
		s.agents.endRunLease(ctx, lease, w, reason)
	}
}

// runCrew is RunCrew past its request validation: provision every member,
// drive the topology, record the terminal state, and end every member's lease
// at whichever terminal point the run reaches.
func (s *CrewServer) runCrew(ctx context.Context, req *pb.RunCrewRequest, crew *pb.Crew, runID string, deps crewRunDeps) (*pb.RunCrewResponse, error) {
	trace := genTraceID()
	run := &pb.CrewRun{
		Id:        runID,
		CrewId:    crew.Id,
		TraceId:   trace,
		State:     pb.CrewRunState_CREW_RUN_STATE_RUNNING,
		InputJson: req.InputJson,
		// Which daemon is driving it (#1322). Startup reconciliation fails
		// runs that are its own or unowned and leaves a peer's alone; without
		// this every run is unowned and a restart fails all of them, on every
		// daemon sharing the database.
		Owner: s.owner,
		// Echo the request's git fields (cloud#1554) — empty on a run with
		// no git_source, unchanged from today. git_commit is filled in below,
		// once the first member's fetch resolves one.
		GitSource: req.GetGitSource(),
		GitRef:    req.GetGitRef(),
		// What each member will run with (#3259), recorded with the run so a
		// client can show the model that ran.
		MemberModels: crewMemberModels(crew.GetSkillIds(), req, s.skillByID),
	}

	// Record the run before driving it, so an in-flight run is observable.
	//
	// Previously the first write happened only after driveCrew returned, so
	// GetCrewRun answered NotFound for the entire duration of a run — the one
	// window where a caller most wants to look — and CREW_RUN_STATE_RUNNING
	// was set on a struct that was overwritten before anyone could read it.
	// The state existed in the proto and never in the store.
	//
	// Safe to store now because the store clones: the mutations below act on
	// the local run, and each put replaces the stored copy.
	if err := s.runs.Put(ctx, run); err != nil {
		log.Printf("[crew] record run %s: %v", run.GetId(), err)
	}

	// Every member's lease, in provisioning order. RunAgentSkill holds ONE
	// lease and ends it with one `defer`; a crew holds one per member and ends
	// all of them at every terminal point below — including the mid-loop
	// provisioning failure, where the members already up are serving with live
	// gateway tokens and a checkout of the caller's repo (#2100).
	leases := make([]runlease.Lease, 0, len(crew.SkillIds))
	// #2799: the coder's box, and the PACK_PRODUCER's lease when the crew has
	// one. validateCrewRoles has already checked the roles' shape.
	roles := crew.GetRoles()
	var coderBox string
	var producer *runlease.Lease

	for i, sid := range crew.SkillIds {
		skill, _ := s.skillByID(sid) // existence already checked by validateCrewTopology
		// #3259: a per-run model wins over the skill's own, applied once here so
		// every provisioning path (member, producer) and the serve command see it.
		skill = withModelOverride(skill, req)
		role := pb.CrewRole_CREW_ROLE_UNSPECIFIED
		if i < len(roles) {
			role = roles[i]
		}
		provision := deps.provision
		if role == pb.CrewRole_CREW_ROLE_PACK_PRODUCER {
			provision = deps.provisionProducer
		}
		// The crew run's own id IS the run id (#1817): every member box's
		// credentials carry it in their `run_id` claim, and the lease's issue
		// audit row resolves back to this one handle — as, now, does its
		// matching agent.run_lease_end row.
		var lease runlease.Lease
		var gitCommit string
		err := errors.New("this daemon cannot provision a PACK_PRODUCER")
		if provision != nil {
			lease, gitCommit, err = provision(ctx, skill, req, run.Id)
		}
		if err != nil {
			run.State = pb.CrewRunState_CREW_RUN_STATE_FAILED
			run.Error = fmt.Sprintf("provision skill %q: %v", sid, err)
			if putErr := s.runs.Put(ctx, run); putErr != nil {
				log.Printf("[crew] record run %s: %v", run.GetId(), putErr)
			}
			// The earlier members provisioned and are serving; this RPC IS the
			// run, so nothing will ever come back for them. End their leases
