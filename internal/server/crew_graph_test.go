package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/crews"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func graphRequest(count int) *pb.RunCrewRequest {
	g := &pb.TaskGraph{Goal: "inspect repository"}
	for i := 0; i < count; i++ {
		g.Nodes = append(g.Nodes, &pb.TaskNode{Id: fmt.Sprintf("n%d", i), Goal: "inspect entry points",
			Assignee: &pb.TaskNode_SkillId{SkillId: "code-review"}})
	}
	return &pb.RunCrewRequest{CrewId: "freeform-crew", Graph: g}
}

func graphCrew() *pb.Crew {
	return &pb.Crew{Id: "freeform-crew", Topology: pb.CrewTopology_CREW_TOPOLOGY_FREEFORM}
}

type graphCapStore struct {
	CrewRunStore
	cap int
}

func (s graphCapStore) Put(ctx context.Context, run *pb.CrewRun) error {
	active := 0
	for _, node := range run.NodeResults {
		if node.State == pb.TaskNodeState_TASK_NODE_STATE_RUNNING {
			active++
		}
	}
	if active > s.cap {
		return fmt.Errorf("scheduler admitted %d nodes above cap %d", active, s.cap)
	}
	return s.CrewRunStore.Put(ctx, run)
}

// Waiting until every runner has started before releasing any proves overlap,
// including repeated assignments to the same logical skill.
func TestCrewGraph_IndependentNodesOverlap(t *testing.T) {
	s := NewCrewServer(nil)
	ctx, cancel := context.WithTimeout(ctxAs("admin", true), 5*time.Second)
	defer cancel()
	started, release := make(chan string, 3), make(chan struct{})
	type outcome struct {
		resp *pb.RunCrewResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := s.runCrewGraph(ctx, graphRequest(3), graphCrew(), "graph-overlap",
			func(ctx context.Context, _ *pb.TaskNode, id, _ string, _ *pb.RunCrewRequest) (string, error) {
				started <- id
				select {
				case <-release:
					return "result", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			})
		done <- outcome{resp, err}
	}()
	ids := make(map[string]bool)
	for i := 0; i < 3; i++ {
		select {
		case id := <-started:
			ids[id] = true
		case <-ctx.Done():
			t.Fatal("three independent nodes did not start together")
		}
	}
	close(release)
	result := <-done
	if result.err != nil || len(ids) != 3 || result.resp.Run.State != pb.CrewRunState_CREW_RUN_STATE_COMPLETED {
		t.Fatalf("result=%v err=%v ids=%v", result.resp, result.err, ids)
	}
	for _, r := range result.resp.Run.NodeResults {
		if r.State != pb.TaskNodeState_TASK_NODE_STATE_SUCCEEDED || r.OutputJson != "result" || r.StartedAt == nil || r.FinishedAt == nil {
			t.Fatalf("missing per-node evidence: %v", r)
		}
	}
}

func TestCrewGraph_ConcurrencyCap(t *testing.T) {
	for _, limit := range []uint32{0, 1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var active, peak atomic.Int32
			req := graphRequest(8)
			req.Concurrency = limit
			cap := int64(limit)
			if cap == 0 {
				cap = defaultGraphConcurrency
			}
			s := NewCrewServer(nil)
			s.runs = graphCapStore{CrewRunStore: s.runs, cap: int(cap)}
			ctx, cancel := context.WithTimeout(ctxAs("admin", true), 5*time.Second)
			defer cancel()
			resp, err := s.runCrewGraph(ctx, req, graphCrew(), "graph-cap",
				func(context.Context, *pb.TaskNode, string, string, *pb.RunCrewRequest) (string, error) {
					n := active.Add(1)
					defer active.Add(-1)
					for old := peak.Load(); n > old; old = peak.Load() {
						if peak.CompareAndSwap(old, n) {
							break
						}
					}
					return "done", nil
				})
			if err != nil || resp.Run.State != pb.CrewRunState_CREW_RUN_STATE_COMPLETED || int64(peak.Load()) > cap {
				t.Fatalf("peak=%d cap=%d err=%v", peak.Load(), cap, err)
			}
		})
	}
}

func TestCrewGraph_DependencyFailurePreservesIndependentBranch(t *testing.T) {
	req := graphRequest(4)
	req.Graph.Edges = []*pb.TaskEdge{{NodeId: "n1", DependsOn: "n0"}, {NodeId: "n2", DependsOn: "n1"}}
	resp, err := NewCrewServer(nil).runCrewGraph(ctxAs("admin", true), req, graphCrew(), "graph-failure",
		func(_ context.Context, n *pb.TaskNode, _, _ string, _ *pb.RunCrewRequest) (string, error) {
			if n.Id == "n0" {
				return "", fmt.Errorf("API check failed")
			}
			if n.Id != "n3" {
				t.Errorf("dependent %s must not execute", n.Id)
			}
			return "independent completed", nil
		})
	if err != nil || resp.Run.State != pb.CrewRunState_CREW_RUN_STATE_FAILED {
		t.Fatalf("%v %v", resp, err)
	}
	states := map[string]pb.TaskNodeState{"n0": pb.TaskNodeState_TASK_NODE_STATE_FAILED, "n1": pb.TaskNodeState_TASK_NODE_STATE_BLOCKED, "n2": pb.TaskNodeState_TASK_NODE_STATE_BLOCKED, "n3": pb.TaskNodeState_TASK_NODE_STATE_SUCCEEDED}
	for _, r := range resp.Run.NodeResults {
		if r.State != states[r.NodeId] {
			t.Errorf("node %v", r)
		}
	}
}

func TestCrewGraph_DiamondWaitsForAllSuccessfulDependencies(t *testing.T) {
	req := graphRequest(4)
	req.Graph.Edges = []*pb.TaskEdge{{NodeId: "n1", DependsOn: "n0"}, {NodeId: "n2", DependsOn: "n0"}, {NodeId: "n3", DependsOn: "n1"}, {NodeId: "n3", DependsOn: "n2"}}
	_, err := NewCrewServer(nil).runCrewGraph(ctxAs("admin", true), req, graphCrew(), "graph-diamond",
		func(_ context.Context, n *pb.TaskNode, _, input string, _ *pb.RunCrewRequest) (string, error) {
			var in graphNodeInput
			if err := json.Unmarshal([]byte(input), &in); err != nil {
				return "", err
			}
			if n.Id == "n3" && len(in.Dependencies) != 2 {
				t.Errorf("merge node missing dependencies: %s", input)
			}
			for _, d := range in.Dependencies {
				if d.OutputJSON != d.NodeID {
					t.Errorf("missing predecessor output: %+v", d)
				}
			}
