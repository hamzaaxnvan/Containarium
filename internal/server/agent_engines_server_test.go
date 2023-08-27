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
		t.Errorf("key_owner = %q, want user:alice", resp.GetKeyOwner())
	}
	codex := statusFor(resp.GetEngines(), pb.AgentEngine_AGENT_ENGINE_CODEX)
	if codex.GetReadiness() != pb.AgentEngineReadiness_AGENT_ENGINE_READINESS_READY {
		t.Errorf("codex readiness = %v, want READY (alice has her own openai key)", codex.GetReadiness())
	}
	// gemini also has no global key, so Statuses checks its owner key too —
	// only openai's lookup matters here; just confirm it happened as alice.
	found := false
	for _, call := range resolver.calls {
		if call == "user:alice|openai" {
			found = true
		}
	}
	if !found {
		t.Errorf("resolver calls = %v, want user:alice|openai among them", resolver.calls)
	}
	if got := codex.GetSkillIds(); len(got) != 1 || got[0] != "codex-skill" {
		t.Errorf("codex skill_ids = %v, want [codex-skill]", got)
	}
}

// TestListAgentEngines_AdminSeesGlobalView pins the other half: "an admin
// with no owner scope sees the global view" — keyOwner is empty, so
// readiness reflects ONLY the global provider set, and a per-owner key that
// exists for some other user never leaks into the admin's rows.
func TestListAgentEngines_AdminSeesGlobalView(t *testing.T) {
	resolver := &fakeOwnerKeyResolverForEngines{has: map[string]bool{"user:alice|openai": true}}
	s := &AgentSkillServer{
		catalog: enginesFixtureCatalog(t),
		gateway: &gatewayProvisioning{engines: agentengine.Gateway{
			DefaultProvider: "anthropic",
			GlobalProviders: map[string]bool{"anthropic": true},
			Keys:            resolver,
		}},
	}
	resp, err := s.ListAgentEngines(ctxAs("ops", true), &pb.ListAgentEnginesRequest{})
	if err != nil {
		t.Fatalf("ListAgentEngines: %v", err)
	}
	if resp.GetKeyOwner() != "" {
