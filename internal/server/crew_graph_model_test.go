package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/pkg/core/crews"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func skillNode(id, skill string) *pb.TaskNode {
	return &pb.TaskNode{Id: id, Goal: "g", Assignee: &pb.TaskNode_SkillId{SkillId: skill}}
}

// A graph run's override scope is the skills its nodes use: skill nodes and the
// members of crew nodes, each once, unknown crews left to graph validation.
func TestGraphModelMembers(t *testing.T) {
	g := &pb.TaskGraph{Nodes: []*pb.TaskNode{
		skillNode("a", "hello-agent-claude"),
		skillNode("b", "hello-agent-claude"),
		{Id: "c", Goal: "g", Assignee: &pb.TaskNode_CrewId{CrewId: "two-engine-crew"}},
		{Id: "d", Goal: "g", Assignee: &pb.TaskNode_CrewId{CrewId: "no-such-crew"}},
	}}
	got := graphModelMembers(g, crews.GetDefault())
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	if seen["hello-agent-claude"] != 1 || seen["hello-agent-codex"] != 1 {
		t.Errorf("members = %v, want each skill of the graph listed once", got)
	}
}

func TestValidateModelOverrides_Graph(t *testing.T) {
	_, get := modelTestSkills()
	members := []string{"named-bare", "no-engine"}
	for _, tc := range []struct {
		name string
		req  *pb.RunCrewRequest
		want codes.Code
	}{
		{"a node skill with a valid id", &pb.RunCrewRequest{ModelOverrides: map[string]string{"named-bare": "m-1"}}, codes.OK},
		{"an invalid id", &pb.RunCrewRequest{ModelOverrides: map[string]string{"named-bare": "bad id"}}, codes.InvalidArgument},
		{"a skill no node uses", &pb.RunCrewRequest{ModelOverrides: map[string]string{"named-pinned": "m-1"}}, codes.InvalidArgument},
		{"a node with no named engine", &pb.RunCrewRequest{ModelOverrides: map[string]string{"no-engine": "m-1"}}, codes.FailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateModelOverrides("this graph", members, tc.req, get); status.Code(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.want)
			}
		})
	}
}

// The single-skill node request carries the same optional model.
func TestSkillRunModel(t *testing.T) {
	_, get := modelTestSkills()
	named, _ := get("named-pinned")
	bare, _ := get("no-engine")
	none := pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED

	if err := validateSkillRunModel(named, none, "m-1"); err != nil {
		t.Errorf("named engine: %v", err)
	}
	if err := validateSkillRunModel(bare, none, ""); err != nil {
		t.Errorf("absent model must pass: %v", err)
	}
	if code := status.Code(validateSkillRunModel(named, none, "x; y")); code != codes.InvalidArgument {
		t.Errorf("invalid id code = %v, want InvalidArgument", code)
	}
	if code := status.Code(validateSkillRunModel(bare, none, "m-1")); code != codes.FailedPrecondition {
		t.Errorf("no named engine code = %v, want FailedPrecondition", code)
	}
	if err := validateSkillRunModel(bare, pb.AgentEngine_AGENT_ENGINE_CODEX, "m-1"); err != nil {
		t.Errorf("engine named on the request must satisfy the rule: %v", err)
	}

	if got := withSkillModel(named, "m-1"); got.GetModel() != "m-1" || named.GetModel() != "pinned-model" {
		t.Errorf("override = %q, catalog skill = %q: want the copy overridden and the shared skill untouched", got.GetModel(), named.GetModel())
