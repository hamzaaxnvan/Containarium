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
func TestQueueRPC_EndToEndLoop(t *testing.T) {
	s := newQueueServer()
	ctx := runScopedCtx()

	// Producer enqueues.
	enq, err := s.EnqueueAgentTask(ctx, &pb.EnqueueAgentTaskRequest{
		SkillId:   "hello-agent",
		InputJson: `{"q":"hi"}`,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if enq.TaskId == "" {
		t.Fatal("enqueue returned empty task id")
	}

	// Worker leases.
	lease, err := s.LeaseAgentTask(ctx, &pb.LeaseAgentTaskRequest{
		WorkerId: "worker-1", SkillId: "hello-agent", LeaseSeconds: 60,
	})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if !lease.HasTask || lease.TaskId != enq.TaskId || lease.InputJson != `{"q":"hi"}` || lease.LeaseToken == "" {
		t.Fatalf("lease returned wrong task: %+v", lease)
	}

	// Worker completes.
	done, err := s.CompleteAgentTask(ctx, &pb.CompleteAgentTaskRequest{
		TaskId: lease.TaskId, LeaseToken: lease.LeaseToken, ArtifactJson: `{"ok":true}`,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !done.Accepted {
		t.Fatal("complete should be accepted with the current lease token")
	}

	// Queue is now empty.
	empty, err := s.LeaseAgentTask(ctx, &pb.LeaseAgentTaskRequest{WorkerId: "worker-1"})
	if err != nil {
		t.Fatalf("lease (empty): %v", err)
	}
	if empty.HasTask {
		t.Fatal("queue should be empty after completion")
	}
}

// TestQueueRPC_StaleCompleteRejected proves the cross-RPC stale-lease guard: a
// worker whose lease expired and was redelivered cannot clobber the retry.
func TestQueueRPC_StaleCompleteRejected(t *testing.T) {
	s := newQueueServer()
	ctx := runScopedCtx()
	base := time.Unix(1_700_000_000, 0).UTC()
	clock := base
	s.queue.(*MemAgentTaskQueue).setClock(func() time.Time { return clock })

	_, _ = s.EnqueueAgentTask(ctx, &pb.EnqueueAgentTaskRequest{SkillId: "hello-agent"})
	first, _ := s.LeaseAgentTask(ctx, &pb.LeaseAgentTaskRequest{LeaseSeconds: 1})

	// Force redelivery by expiring the lease, then re-lease (new token).
	clock = base.Add(2 * time.Second)
	second, _ := s.LeaseAgentTask(ctx, &pb.LeaseAgentTaskRequest{LeaseSeconds: 60})
	if second.LeaseToken == first.LeaseToken {
		t.Fatal("redelivery should mint a new token")
	}

	stale, _ := s.CompleteAgentTask(ctx, &pb.CompleteAgentTaskRequest{
