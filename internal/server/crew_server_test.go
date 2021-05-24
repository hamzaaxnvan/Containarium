package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/crews"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func completed(out string) *pb.SendAgentTaskResponse {
	return &pb.SendAgentTaskResponse{Artifact: &pb.AgentArtifact{
		OutputJson: out, State: pb.AgentTaskState_AGENT_TASK_STATE_COMPLETED,
	}}
}

// TestRunCrew_RunIDValidatedBeforeCrewLookup is #1899: a malformed
// caller-supplied run_id must fail the RPC before any catalog/topology work,
// same posture as RunAgentSkill's resolveRunID check. Uses NewCrewServer(nil)
// (no agents server) — reachable because run_id resolution happens before
// s.agents is ever touched.
func TestRunCrew_RunIDValidatedBeforeCrewLookup(t *testing.T) {
	s := NewCrewServer(nil)
	ctx := ctxAs("admin", true)

	// A bad run_id is rejected even for an unknown crew — proves resolveRunID
	// runs before the catalog lookup, not after.
	_, err := s.RunCrew(ctx, &pb.RunCrewRequest{CrewId: "no-such-crew", RunId: "bad id with spaces"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad run_id: code = %v, want InvalidArgument", status.Code(err))
	}

	// A well-formed run_id is not rejected at this boundary — the request
	// proceeds to the (failing) crew lookup instead.
	_, err = s.RunCrew(ctx, &pb.RunCrewRequest{CrewId: "no-such-crew", RunId: "run-ok"})
	if status.Code(err) == codes.InvalidArgument {
		t.Errorf("valid run_id must not be rejected: %v", err)
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown crew: code = %v, want NotFound", status.Code(err))
	}
}

// TestRunCrew_ResponseCarriesRunID pins the run_id half of #1899's contract
// against the GENERATED pb types (RunCrewRequest.run_id, CrewRun.id), so a
// stale regeneration fails to compile rather than passing quietly — same
// rationale as TestRunAgentSkill_ResponseCarriesRunID. The full RPC can't be
// driven to COMPLETED from a unit test (box provisioning needs a real
// backend); what's pinned here is that a resolved run id round-trips into the
// CrewRun the same way RunCrew's own code constructs it (Id: runID).
func TestRunCrew_ResponseCarriesRunID(t *testing.T) {
	for _, in := range []string{"", "run-echoed"} {
		runID, err := resolveRunID(in)
		if err != nil {
			t.Fatalf("resolveRunID(%q): %v", in, err)
		}
		run := &pb.CrewRun{Id: runID}
		if run.GetId() != runID {
			t.Errorf("run id = %q, want %q", run.GetId(), runID)
		}
		if in != "" && run.GetId() != in {
			t.Errorf("run id = %q, want the caller's %q echoed", run.GetId(), in)
		}
	}
}

// TestValidateTaskGraph is #2803: the RPC validates against the daemon's own
// crew + skill catalogs and maps a rejected graph to InvalidArgument.
func TestValidateTaskGraph(t *testing.T) {
	s := NewCrewServer(nil)
	ctx := ctxAs("admin", true)

	resp, err := s.ValidateTaskGraph(ctx, &pb.ValidateTaskGraphRequest{Graph: &pb.TaskGraph{
		Nodes: []*pb.TaskNode{
			{Id: "review", Goal: "review", Assignee: &pb.TaskNode_SkillId{SkillId: "code-review"}},
			{Id: "build", Goal: "build", Assignee: &pb.TaskNode_CrewId{CrewId: "hello-crew"}},
		},
		Edges: []*pb.TaskEdge{{NodeId: "review", DependsOn: "build"}},
	}})
	if err != nil {
		t.Fatalf("valid graph: %v", err)
	}
	if strings.Join(resp.GetOrder(), ",") != "build,review" || resp.GetDepth() != 2 {
		t.Errorf("got order %v depth %d, want [build review] depth 2", resp.GetOrder(), resp.GetDepth())
	}

	_, err = s.ValidateTaskGraph(ctx, &pb.ValidateTaskGraphRequest{Graph: &pb.TaskGraph{
		Nodes: []*pb.TaskNode{{Id: "a", Goal: "a", Assignee: &pb.TaskNode_SkillId{SkillId: "no-such-skill"}}},
	}})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "no-such-skill") {
		t.Errorf("unknown skill: err = %v, want InvalidArgument naming the skill", err)
	}
}

func TestDriveCrew(t *testing.T) {
	const trace = "trace-xyz"

	t.Run("pipeline chains outputs, threads from + trace", func(t *testing.T) {
		var calls []*pb.SendAgentTaskRequest
		send := func(_ context.Context, req *pb.SendAgentTaskRequest) (*pb.SendAgentTaskResponse, error) {
			calls = append(calls, req)
			return completed("out-of-" + req.ToPeerId), nil
		}
		crew := &pb.Crew{Id: "c", Topology: pb.CrewTopology_CREW_TOPOLOGY_PIPELINE, SkillIds: []string{"relay", "hello"}}

		out, err := driveCrew(context.Background(), crew, trace, "seed-input", send)
		if err != nil {
			t.Fatalf("driveCrew: %v", err)
		}
		if out != "out-of-hello" {
			t.Errorf("final output = %q, want out-of-hello", out)
		}
		if len(calls) != 2 {
			t.Fatalf("expected 2 hops, got %d", len(calls))
		}
		if calls[0].FromSkillId != "" || calls[0].ToPeerId != "relay" || calls[0].InputJson != "seed-input" {
			t.Errorf("hop1 = %+v", calls[0])
		}
		if calls[1].FromSkillId != "relay" || calls[1].ToPeerId != "hello" || calls[1].InputJson != "out-of-relay" {
			t.Errorf("hop2 = %+v (want from=relay to=hello input=out-of-relay)", calls[1])
		}
		for i, c := range calls {
			if c.TraceId != trace {
				t.Errorf("hop %d trace = %q, want %q", i, c.TraceId, trace)
			}
		}
	})

	// #2799: the PACK_PRODUCER is not an A2A peer; the pipeline ends at the
	// reviewer and the producer is never handed a task.
	t.Run("pipeline skips the pack producer", func(t *testing.T) {
		var to []string
		send := func(_ context.Context, req *pb.SendAgentTaskRequest) (*pb.SendAgentTaskResponse, error) {
			to = append(to, req.ToPeerId)
			return completed("out-of-" + req.ToPeerId), nil
		}
		crew := &pb.Crew{Id: "c", Topology: pb.CrewTopology_CREW_TOPOLOGY_PIPELINE,
			SkillIds: []string{"coder", "reviewer", "runner"},
			Roles:    []pb.CrewRole{pb.CrewRole_CREW_ROLE_CODER, pb.CrewRole_CREW_ROLE_REVIEWER, pb.CrewRole_CREW_ROLE_PACK_PRODUCER}}
		out, err := driveCrew(context.Background(), crew, trace, "seed-input", send)
		if err != nil {
			t.Fatalf("driveCrew: %v", err)
		}
		if strings.Join(to, ",") != "coder,reviewer" || out != "out-of-reviewer" {
			t.Errorf("hops = %v, output = %q; want coder,reviewer and the reviewer's output", to, out)
		}
	})

	t.Run("failed hop surfaces the peer error", func(t *testing.T) {
		send := func(_ context.Context, _ *pb.SendAgentTaskRequest) (*pb.SendAgentTaskResponse, error) {
			return &pb.SendAgentTaskResponse{Artifact: &pb.AgentArtifact{
				State: pb.AgentTaskState_AGENT_TASK_STATE_FAILED, Error: "boom",
			}}, nil
		}
		crew := &pb.Crew{Id: "c", Topology: pb.CrewTopology_CREW_TOPOLOGY_PIPELINE, SkillIds: []string{"a", "b"}}
		if _, err := driveCrew(context.Background(), crew, trace, "x", send); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("expected the peer failure to surface, got %v", err)
		}
	})

	t.Run("transport error propagates", func(t *testing.T) {
		send := func(_ context.Context, _ *pb.SendAgentTaskRequest) (*pb.SendAgentTaskResponse, error) {
			return nil, errors.New("unavailable")
		}
		crew := &pb.Crew{Id: "c", Topology: pb.CrewTopology_CREW_TOPOLOGY_PIPELINE, SkillIds: []string{"a", "b"}}
		if _, err := driveCrew(context.Background(), crew, trace, "x", send); err == nil {
			t.Error("expected transport error to propagate")
		}
	})

	t.Run("orchestrator delivers to the entry skill only", func(t *testing.T) {
		var calls []*pb.SendAgentTaskRequest
		send := func(_ context.Context, req *pb.SendAgentTaskRequest) (*pb.SendAgentTaskResponse, error) {
			calls = append(calls, req)
			return completed("coordinated"), nil
		}
		crew := &pb.Crew{Id: "c", Topology: pb.CrewTopology_CREW_TOPOLOGY_ORCHESTRATOR, SkillIds: []string{"coord", "w1", "w2"}}
		out, err := driveCrew(context.Background(), crew, trace, "x", send)
		if err != nil {
			t.Fatalf("driveCrew: %v", err)
		}
		if out != "coordinated" {
			t.Errorf("output = %q", out)
		}
		if len(calls) != 1 || calls[0].ToPeerId != "coord" {
			t.Errorf("expected one delivery to coord, got %d calls (%+v)", len(calls), calls)
		}
	})
}

// TestEmbeddedFreeformCrewValidatesAndDrives proves the embedded freeform-crew
// (#1552) is actually exercisable end-to-end: it clears RunCrew's topology
// gate against the real skill catalog (not a fake), and driveCrew delivers to
// its entry skill and returns that skill's artifact, same as a real RunCrew
// would. Without this crew, #1548's scenario has no freeform topology to run.
func TestEmbeddedFreeformCrewValidatesAndDrives(t *testing.T) {
	crew, err := crews.GetDefault().Get("freeform-crew")
	if err != nil {
		t.Fatalf("freeform-crew missing from embedded catalog: %v", err)
	}
	if crew.Topology != pb.CrewTopology_CREW_TOPOLOGY_FREEFORM {
		t.Fatalf("freeform-crew topology = %v, want FREEFORM", crew.Topology)
	}
	if len(crew.SkillIds) < 2 {
		t.Fatalf("freeform-crew should reference >=2 skills, got %v", crew.SkillIds)
	}

	sk := skills.GetDefault()
	getSkill := func(id string) (*pb.AgentSkill, bool) {
		s, err := sk.Get(id)
		if err != nil {
			return nil, false
		}
		return s, true
	}
	if err := validateCrewTopology(crew, getSkill); err != nil {
		t.Errorf("RunCrew's topology gate rejects the embedded freeform crew: %v", err)
	}

	var calls []*pb.SendAgentTaskRequest
	send := func(_ context.Context, req *pb.SendAgentTaskRequest) (*pb.SendAgentTaskResponse, error) {
		calls = append(calls, req)
		return completed("done"), nil
	}
	out, err := driveCrew(context.Background(), crew, "trace-1", "seed", send)
	if err != nil {
		t.Fatalf("driveCrew: %v", err)
