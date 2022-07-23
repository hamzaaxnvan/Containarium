package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/crews"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const validPlannerGraph = `{"goal":"review API","nodes":[{"id":"review","goal":"inspect API entry points","skill_id":"code-review"}]}`

type plannerContextStore struct{ CrewRunStore }

func (s plannerContextStore) Put(ctx context.Context, run *pb.CrewRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.CrewRunStore.Put(ctx, run)
}

func TestPlanTaskGraph_OnlyRunsPlannerAndValidatesBeforeReturning(t *testing.T) {
	s := NewCrewServer(nil)
	ctx := ctxAs("admin", true)
	req := &pb.PlanTaskGraphRequest{Goal: "review API", Repo: "https://example.com/org/repo.git",
		GitRef: "pinned-ref", GitCredential: "fetch-secret", BackendId: "backend", Pool: "pool"}
	var called int
	resp, err := s.planTaskGraph(ctx, req, func(_ context.Context, task *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
		called++
		if task.SkillId != plannerSkillID || task.GetEngine() != pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED {
			t.Fatalf("must run only the planner with the primary provider: %v", task)
		}
		if task.GitSource != req.Repo || task.GitRef != req.GitRef || task.GitCredential != req.GitCredential || task.BackendId != req.BackendId || task.Pool != req.Pool {
			t.Fatal("planner must inspect the requested repo/ref with the requested placement")
		}
		var input plannerInput
		if err := json.Unmarshal([]byte(task.InputJson), &input); err != nil {
			t.Fatal(err)
		}
		if input.Goal != req.Goal || input.MaxNodes != 32 || input.MaxDepth != 8 || len(input.Crews) == 0 {
			t.Fatalf("missing planning context: %+v", input)
		}
		found := false
		for _, sk := range input.Skills {
			if sk.ID == plannerSkillID {
				t.Fatal("planner must not assign itself recursive planning work")
			}
			found = found || sk.ID == "code-review"
		}
		if !found {
			t.Fatal("planner needs the daemon's actual assignee catalog")
		}
		return &pb.RunAgentSkillResponse{ArtifactJson: validPlannerGraph, GitCommit: "resolved-sha"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || resp.Graph.Nodes[0].GetSkillId() != "code-review" || resp.GitCommit != "resolved-sha" {
		t.Fatalf("unexpected planning result: calls=%d, response=%v", called, resp)
	}
	record, ok, err := s.runs.Get(ctx, resp.RunId)
	if err != nil || !ok || record.State != pb.CrewRunState_CREW_RUN_STATE_COMPLETED || record.ArtifactJson == "" {
		t.Fatalf("missing completed plan record: %v, %v", record, err)
	}
	if strings.Contains(record.String(), "fetch-secret") {
		t.Fatal("fetch credential must not enter the saved plan")
	}
}

func TestPlanTaskGraph_RejectedOutputIsRecordedWithoutExecutingNodes(t *testing.T) {
	cases := map[string]string{
		"self assignee":    `{"goal":"review API","nodes":[{"id":"a","goal":"implement","skill_id":"task-planner"}]}`,
		"malformed":        "not JSON",
		"unknown field":    `{"goal":"review API","nodez":[]}`,
		"unknown assignee": `{"goal":"review API","nodes":[{"id":"a","goal":"g","skill_id":"missing"}]}`,
		"cycle":            `{"goal":"review API","nodes":[{"id":"a","goal":"g","skill_id":"code-review"}],"edges":[{"node_id":"a","depends_on":"a"}]}`,
		"over cap":         `{"goal":"review API","nodes":[{"id":"a","goal":"g","skill_id":"code-review"}],"limits":{"max_nodes":33}}`,
		"wrong goal":       `{"goal":"another goal","nodes":[{"id":"a","goal":"g","skill_id":"code-review"}]}`,
		"empty output":     "",
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewCrewServer(nil) // no executor: a plan must never need one
			ctx := ctxAs("admin", true)
			var runID string
			calls := 0
			resp, err := s.planTaskGraph(ctx, &pb.PlanTaskGraphRequest{Goal: "review API", Repo: "https://example.com/repo.git"},
				func(_ context.Context, task *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
					calls++
					runID = task.RunId
					if task.SkillId != plannerSkillID {
						t.Fatal("must not execute an output node")
					}
					return &pb.RunAgentSkillResponse{ArtifactJson: output}, nil
				})
			if resp != nil || status.Code(err) != codes.FailedPrecondition || calls != 1 || !strings.Contains(err.Error(), runID) {
				t.Fatalf("invalid plan escaped rejection: response=%v error=%v calls=%d", resp, err, calls)
			}
			if name == "self assignee" && (!strings.Contains(err.Error(), `node "a"`) || !strings.Contains(err.Error(), "planner")) {
				t.Fatalf("self-assignment error must identify the node: %v", err)
			}
			record, ok, storeErr := s.runs.Get(ctx, runID)
			if storeErr != nil || !ok || record.State != pb.CrewRunState_CREW_RUN_STATE_FAILED || record.Error == "" || record.ArtifactJson != "" {
				t.Fatalf("failure reason must be retained, not a usable artifact: %v %v", record, storeErr)
			}
		})
	}
}
