package server

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/skills"
)

// The request a graph skill node is started with carries that skill's model
// override, its engine override, and the parent's placement and git fields.
func TestGraphSkillNodeRequest_CarriesModelAndEngine(t *testing.T) {
	parent := &pb.RunCrewRequest{
		BackendId: "b", Pool: "p", GitSource: "src", GitRef: "ref", GitCredential: "cred",
		EngineOverrides: map[string]pb.AgentEngine{"s1": pb.AgentEngine_AGENT_ENGINE_CODEX},
		ModelOverrides:  map[string]string{"s1": "m-1", "other": "m-2"},
	}
	got := graphSkillNodeRequest(parent, "s1", "run-1", `{"x":1}`)
	if got.GetModel() != "m-1" {
		t.Errorf("model = %q, want the node skill's override", got.GetModel())
	}
	if got.GetEngine() != pb.AgentEngine_AGENT_ENGINE_CODEX {
		t.Errorf("engine = %v, want the skill's engine override", got.GetEngine())
	}
	if got.GetSkillId() != "s1" || got.GetRunId() != "run-1" || got.GetInputJson() != `{"x":1}` ||
		got.GetBackendId() != "b" || got.GetPool() != "p" || got.GetGitSource() != "src" ||
		got.GetGitRef() != "ref" || got.GetGitCredential() != "cred" {
		t.Errorf("request = %v", got)
	}
	if none := graphSkillNodeRequest(parent, "unlisted", "r", ""); none.GetModel() != "" {
		t.Errorf("a skill with no override got model %q", none.GetModel())
	}
}

// A crew node's child request keeps the overrides (runCrew applies them to the
// members) and drops the graph fields, and the parent request is not mutated.
func TestGraphCrewNodeRequest_KeepsOverridesDropsGraph(t *testing.T) {
	parent := &pb.RunCrewRequest{
		CrewId: "outer", Graph: &pb.TaskGraph{Goal: "g"}, Concurrency: 3,
		ModelOverrides: map[string]string{"s1": "m-1"},
	}
	got := graphCrewNodeRequest(parent, &pb.Crew{Id: "inner"}, "run-2", "in")
	if got.GetModelOverrides()["s1"] != "m-1" {
		t.Errorf("model_overrides = %v, want the parent's carried to the crew node", got.GetModelOverrides())
	}
	if got.GetGraph() != nil || got.GetConcurrency() != 0 {
		t.Errorf("graph fields survived: %v", got)
