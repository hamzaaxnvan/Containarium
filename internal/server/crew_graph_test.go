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
