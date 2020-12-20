package taskgraph

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeSkills map[string]bool

func (f fakeSkills) Get(id string) (*pb.AgentSkill, error) {
	if !f[id] {
		return nil, fmt.Errorf("skill not found: %s", id)
	}
	return &pb.AgentSkill{Id: id}, nil
}

type fakeCrews map[string]bool

func (f fakeCrews) Get(id string) (*pb.Crew, error) {
	if !f[id] {
		return nil, fmt.Errorf("crew not found: %s", id)
	}
	return &pb.Crew{Id: id}, nil
}

var (
	knownSkills = fakeSkills{"coder": true, "reviewer": true}
	knownCrews  = fakeCrews{"build-crew": true}
)

func skillNode(id, skill string) *pb.TaskNode {
	return &pb.TaskNode{Id: id, Goal: "do " + id, Assignee: &pb.TaskNode_SkillId{SkillId: skill}}
}

func crewNode(id, crew string) *pb.TaskNode {
	return &pb.TaskNode{Id: id, Goal: "do " + id, Assignee: &pb.TaskNode_CrewId{CrewId: crew}}
}

func edge(node, dependsOn string) *pb.TaskEdge {
	return &pb.TaskEdge{NodeId: node, DependsOn: dependsOn}
}

// Resource minima must be structurally valid even when validation runs on a
// client without a host profile. Capacity eligibility is checked at execution.
func TestValidate_NodeResources(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resources *pb.TaskNodeResources
		invalid   bool
	}{
		{"absent", nil, false},
		{"empty", &pb.TaskNodeResources{}, false},
		{"cpu", &pb.TaskNodeResources{MinCpuCores: 4}, false},
		{"memory", &pb.TaskNodeResources{MinMemoryBytes: 8 << 30}, false},
		{"gpu", &pb.TaskNodeResources{RequiresGpu: true}, false},
		{"negative cpu", &pb.TaskNodeResources{MinCpuCores: -1}, true},
		{"negative memory", &pb.TaskNodeResources{MinMemoryBytes: -1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, node := range []*pb.TaskNode{skillNode("resource-node", "coder"), crewNode("resource-node", "build-crew")} {
				node.Resources = tc.resources
				_, err := Validate(&pb.TaskGraph{Nodes: []*pb.TaskNode{node}}, knownSkills, knownCrews)
				if tc.invalid {
					if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), node.Id) {
						t.Fatalf("invalid requirements must identify the node: %v", err)
					}
				} else if err != nil {
					t.Fatalf("valid resource requirements rejected: %v", err)
				}
			}
		})
	}
}

// chain returns n skill nodes where each depends on the one before it.
func chain(n int) *pb.TaskGraph {
	g := &pb.TaskGraph{Goal: "chain"}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("n%02d", i)
		g.Nodes = append(g.Nodes, skillNode(id, "coder"))
		if i > 0 {
			g.Edges = append(g.Edges, edge(id, fmt.Sprintf("n%02d", i-1)))
		}
	}
	return g
}

func TestValidateAcceptsDiamond(t *testing.T) {
	g := &pb.TaskGraph{
		Goal: "ship a feature",
		Nodes: []*pb.TaskNode{
			skillNode("design", "coder"),
			skillNode("backend", "coder"),
			crewNode("frontend", "build-crew"),
			skillNode("review", "reviewer"),
		},
		Edges: []*pb.TaskEdge{
			edge("backend", "design"),
			edge("frontend", "design"),
			edge("review", "backend"),
			edge("review", "frontend"),
			edge("review", "frontend"), // repeated edges are tolerated
		},
	}
	res, err := Validate(g, knownSkills, knownCrews)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if want := []string{"design", "backend", "frontend", "review"}; !reflect.DeepEqual(res.Order, want) {
		t.Errorf("Order = %v, want %v", res.Order, want)
	}
	if res.Depth != 3 {
		t.Errorf("Depth = %d, want 3", res.Depth)
	}
}

func TestValidateIndependentNodesHaveDepthOne(t *testing.T) {
	g := &pb.TaskGraph{Nodes: []*pb.TaskNode{skillNode("b", "coder"), skillNode("a", "reviewer")}}
	res, err := Validate(g, knownSkills, knownCrews)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(res.Order, want) {
