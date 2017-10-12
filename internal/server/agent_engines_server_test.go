package server

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// enginesFixtureCatalog loads a small, controlled catalog (not the embedded
// default) so these tests assert on exactly the skills they name, independent
// of whatever the real catalog ships.
func enginesFixtureCatalog(t *testing.T) *skills.Manager {
	t.Helper()
	m := skills.New()
	if err := m.LoadFromBytes([]byte(`
skills:
  - id: codex-skill
    recipe_id: agent-runtime
    system_prompt: hi
    allowed_scopes: [containers:read]
    engine: codex
  - id: unspecified-skill
    recipe_id: agent-runtime
    system_prompt: hi
    allowed_scopes: [containers:read]
`)); err != nil {
		t.Fatalf("load fixture catalog: %v", err)
	}
	return m
}

func statusFor(rows []*pb.AgentEngineStatus, e pb.AgentEngine) *pb.AgentEngineStatus {
	for _, r := range rows {
		if r.GetEngine() == e {
			return r
		}
	}
	return nil
}

func TestListAgentEngines_DirectMode(t *testing.T) {
	s := &AgentSkillServer{catalog: enginesFixtureCatalog(t)}
	resp, err := s.ListAgentEngines(ctxAs("alice", false), &pb.ListAgentEnginesRequest{})
	if err != nil {
		t.Fatalf("ListAgentEngines: %v", err)
	}
	if len(resp.GetEngines()) != len(agentengine.All()) {
		t.Fatalf("rows = %d, want %d", len(resp.GetEngines()), len(agentengine.All()))
	}
	for _, row := range resp.GetEngines() {
		if row.GetReadiness() != pb.AgentEngineReadiness_AGENT_ENGINE_READINESS_UNKNOWN_DIRECT_MODE {
			t.Errorf("engine %v: readiness = %v, want UNKNOWN_DIRECT_MODE (no gateway configured)", row.GetEngine(), row.GetReadiness())
		}
	}
}

// TestListAgentEngines_TenantSeesOwnOwner pins the per-owner resolution rule
// from the issue: "a tenant sees readiness for their key owner" — a
// non-admin subject's keyOwner is UserKeyOwner(subject), matching
// MintGatewayToken's own resolution, never the empty/global view.
func TestListAgentEngines_TenantSeesOwnOwner(t *testing.T) {
	resolver := &fakeOwnerKeyResolverForEngines{has: map[string]bool{"user:alice|openai": true}}
	s := &AgentSkillServer{
		catalog: enginesFixtureCatalog(t),
		gateway: &gatewayProvisioning{engines: agentengine.Gateway{
			DefaultProvider: "anthropic",
			GlobalProviders: map[string]bool{"anthropic": true},
			Keys:            resolver,
		}},
	}
	resp, err := s.ListAgentEngines(ctxAs("alice", false), &pb.ListAgentEnginesRequest{})
	if err != nil {
		t.Fatalf("ListAgentEngines: %v", err)
	}
	if resp.GetKeyOwner() != "user:alice" {
