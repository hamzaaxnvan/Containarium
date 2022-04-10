package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/taskgraph"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const plannerSkillID = "task-planner"

type plannerAssignee struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type plannerInput struct {
	Goal     string            `json:"goal"`
	Skills   []plannerAssignee `json:"skills"`
	Crews    []plannerAssignee `json:"crews"`
	MaxNodes int               `json:"max_nodes"`
	MaxDepth int               `json:"max_depth"`
}

// PlanTaskGraph reuses skill provisioning and lease cleanup. The injected
// runner in planTaskGraph lets tests exercise validation without a real box.
func (s *CrewServer) PlanTaskGraph(ctx context.Context, req *pb.PlanTaskGraphRequest) (*pb.PlanTaskGraphResponse, error) {
	return s.planTaskGraph(ctx, req, s.runPlannerSkill)
}

func (s *CrewServer) runPlannerSkill(ctx context.Context, req *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
	if s.agents == nil {
		return nil, status.Error(codes.FailedPrecondition, "agent runtime is unavailable")
	}
	run, err := s.agents.beginSkillRun(ctx, req)
	if err != nil {
		return nil, err
	}
	defer s.agents.endRunLease(ctx, run.lease, s.agents.boxWiper(), runExitReason)
	// Planning requires an artifact; use the standalone run's status mapping
	// so a missing runtime remains distinguishable from execution failure.
	out, err := s.agents.runInBoxAgentResult(run.containerName, run.lease.SeedDir, run.runID, run.skillID, run.engineRes, run.model)
