package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// runScopedCtx returns a context carrying an agents:run credential, the scope a
// worker's minted queue token grants.
func runScopedCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(
		context.Background(), "agent-hello-agent", nil, []string{auth.ScopeAgentsRun})
}

func newQueueServer() *AgentSkillServer {
	return &AgentSkillServer{catalog: skills.GetDefault(), queue: NewMemAgentTaskQueue()}
}

// TestQueueRPC_EndToEndLoop drives the full producer→worker loop through the
// real RPC handlers with a real agents:run credential context — the same path
// a poll-mode worker exercises, minus the HTTP transport. This is the
// end-to-end proof that the minted credential authorizes the lease/complete
// cycle and the queue threads a task from enqueue to completion.
