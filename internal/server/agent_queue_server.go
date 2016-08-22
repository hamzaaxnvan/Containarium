package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// agentWorkerDaemonPort is the daemon's REST (grpc-gateway) port, where a
// worker box reaches the agent-tasks endpoints. The worker resolves the host
// (its default gateway) at launch and dials this port. Configurable later;
// hard-coded to the daemon's well-known HTTP port for the prototype.
const agentWorkerDaemonPort = 8080

// agent_queue_server.go — the gRPC surface of the pull-based run queue
// (prototype). Thin handlers over agentTaskQueue; the queue type holds the
// semantics and the tests. All three gate on agents:run — producing, leasing,
// and completing queued work are all "operate the agent runtime" actions.
// See docs/AGENT-MODEL-GATEWAY-DESIGN.md (pull-queue section).

// EnqueueAgentTask places a task on the queue for a skill. Validates the skill
// exists so a typo doesn't sit in the queue forever, never leasable.
func (s *AgentSkillServer) EnqueueAgentTask(ctx context.Context, req *pb.EnqueueAgentTaskRequest) (*pb.EnqueueAgentTaskResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAgentsRun); err != nil {
		return nil, err
	}
	if req.SkillId == "" {
		return nil, status.Error(codes.InvalidArgument, "skill_id is required")
	}
	if _, err := s.catalog.Get(req.SkillId); err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
