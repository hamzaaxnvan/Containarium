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
