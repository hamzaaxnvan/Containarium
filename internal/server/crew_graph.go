package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/taskgraph"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultGraphConcurrency = 5

type graphNodeRunner func(context.Context, *pb.TaskNode, string, string, *pb.RunCrewRequest) (string, error)

type graphDependency struct {
	NodeID     string `json:"node_id"`
	OutputJSON string `json:"output_json"`
}

type graphNodeInput struct {
	Goal         string            `json:"goal"`
	InputJSON    string            `json:"input_json"`
	Dependencies []graphDependency `json:"dependencies"`
}

// validateExecutableGraph adds dispatch policy to the generic #2803 validator.
// Both planning and execution apply it: model prompts are not enforcement.
func (s *CrewServer) validateExecutableGraph(g *pb.TaskGraph) (*taskgraph.Result, error) {
	res, err := taskgraph.Validate(g, s.skills, s.catalog)
	if err != nil {
		return nil, err
	}
	for _, node := range g.Nodes {
		if node.GetSkillId() == plannerSkillID {
			return nil, fmt.Errorf("node %q assigns the planner to itself", node.Id)
		}
		if node.GetCrewId() != "" {
			crew, _ := s.catalog.Get(node.GetCrewId())
			for _, id := range crew.SkillIds {
				if id == plannerSkillID {
					return nil, fmt.Errorf("node %q crew contains the planner", node.Id)
				}
			}
			if err := validateCrewTopology(crew, s.skillByID); err != nil {
				return nil, fmt.Errorf("node %q: %w", node.Id, err)
			}
		}
	}
	return res, nil
}

// runCrewGraph validates the entire graph before allocating any boxes. Only
// this goroutine mutates results; workers return values through events.
func (s *CrewServer) runCrewGraph(ctx context.Context, req *pb.RunCrewRequest, crew *pb.Crew, runID string, execute graphNodeRunner) (*pb.RunCrewResponse, error) {
	for _, scope := range []string{auth.ScopeAgentsRun, auth.ScopeAgentsCall, auth.ScopeContainersWrite} {
		if err := auth.RequireScope(ctx, scope); err != nil {
			return nil, err
		}
	}
	if crew.Topology != pb.CrewTopology_CREW_TOPOLOGY_ORCHESTRATOR && crew.Topology != pb.CrewTopology_CREW_TOPOLOGY_FREEFORM {
		return nil, status.Error(codes.InvalidArgument, "only ORCHESTRATOR/FREEFORM accept a task graph")
	}
	validated, err := s.validateExecutableGraph(req.Graph)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "task graph: %v", err)
	}
	limit := int(req.Concurrency)
	if limit == 0 {
		limit = defaultGraphConcurrency
	}
	if limit > taskgraph.MaxNodes {
		return nil, status.Errorf(codes.InvalidArgument, "concurrency must not exceed %d", taskgraph.MaxNodes)
	}
	for _, node := range req.Graph.Nodes {
		if _, err := s.validateLocalNodeResources(node, req); err != nil {
			return nil, err
		}
	}
	run := &pb.CrewRun{Id: runID, CrewId: crew.Id, TraceId: genTraceID(), Owner: s.owner,
		State: pb.CrewRunState_CREW_RUN_STATE_RUNNING, Graph: req.Graph,
		GitSource: req.GitSource, GitRef: req.GitRef,
		// What each node's skill will run with (#3264).
		MemberModels: crewMemberModels(graphModelMembers(req.Graph, s.catalog), req, s.skillByID)}
	nodes := make(map[string]*pb.TaskNode, len(req.Graph.Nodes))
	results := make(map[string]*pb.TaskNodeResult, len(req.Graph.Nodes))
	deps := make(map[string][]string)
	for _, node := range req.Graph.Nodes {
		nodes[node.Id] = node
	}
	for _, id := range validated.Order {
		childID, err := resolveRunID("")
		if err != nil {
			return nil, err
		}
		r := &pb.TaskNodeResult{NodeId: id, RunId: childID, State: pb.TaskNodeState_TASK_NODE_STATE_PENDING}
		results[id] = r
		run.NodeResults = append(run.NodeResults, r)
	}
	for _, edge := range req.Graph.Edges {
		deps[edge.NodeId] = append(deps[edge.NodeId], edge.DependsOn)
	}
	persist := func() error {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return s.runs.Put(wctx, run)
