package server

import (
	"context"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestBuildAgentSeedScript(t *testing.T) {
	seedDir := seedDirFor("run-1")
	script := buildAgentSeedScript(seedDir, "be helpful", "tok-123", `{"q":"hi"}`, `{"id":"x"}`)

	for _, want := range []string{
		"set -euo pipefail",
		"umask 077",
		"mkdir -p " + seedDir,
		seedDir + "/system_prompt.txt",
		seedDir + "/token",
		seedDir + "/input.json",
		seedDir + "/agent-card.json",
		"chmod 600 " + seedDir + "/token",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("seed script missing %q\n---\n%s", want, script)
		}
	}
}

func TestBuildAgentSeedScriptDefaultsInput(t *testing.T) {
	script := buildAgentSeedScript(seedDirFor("run-1"), "p", "t", "", "")
	if !strings.Contains(script, "'{}'") {
		t.Errorf("empty input should default to {}, got:\n%s", script)
	}
}

// TestSendAgentTaskRejectsDisallowedPeer is the moat as a test: an agent whose
// allowed_peers does not include the target is rejected at the API boundary,
// BEFORE any A2A send is attempted. The caller identity is taken from the
// authenticated token subject (agent-<skill-id>), not the caller-asserted
// field, so an agent box can't spoof a different caller to bypass the gate.
func TestSendAgentTaskRejectsDisallowedPeer(t *testing.T) {
	// hello-agent ships with allowed_peers: [] (a leaf), so every peer is denied.
	s := &AgentSkillServer{catalog: skills.GetDefault()}

	// Authenticate as the agent box itself; agents:call scope present.
	ctx := auth.ContextWithTestSubjectScopes(
		context.Background(), "agent-hello-agent", nil, []string{auth.ScopeAgentsCall})

	// from_skill_id deliberately lies ("admin-ish") — the authenticated subject
	// must win, so the call is still denied.
	_, err := s.SendAgentTask(ctx, &pb.SendAgentTaskRequest{
		FromSkillId: "some-privileged-skill",
		ToPeerId:    "other-peer",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for a peer not in allowed_peers, got %v", err)
	}
}

// TestMintedAgentTokenScopes_CallerRestrictedToAgentsRunGetsNoResourceScopes
// is the #1676 regression: a caller holding only agents:run must not receive
// a token carrying a manifest scope it never held itself.
func TestMintedAgentTokenScopes_CallerRestrictedToAgentsRunGetsNoResourceScopes(t *testing.T) {
	skill := &pb.AgentSkill{Id: "hello-agent", AllowedScopes: []string{auth.ScopeContainersRead}}
	ctx := auth.ContextWithTestSubjectScopes(
		context.Background(), "some-caller", nil, []string{auth.ScopeAgentsRun})

	got := mintedAgentTokenScopes(ctx, skill)
	if len(got) != 0 {
		t.Fatalf("mintedAgentTokenScopes = %v, want no resource scopes for a caller holding only agents:run", got)
	}
}

// TestMintedAgentTokenScopes_ManifestScopeCallerLacksNeverGranted is the
// regression named explicitly in #1676's acceptance criteria: a manifest
// declaring a scope the caller doesn't hold must never produce a token
// carrying it.
func TestMintedAgentTokenScopes_ManifestScopeCallerLacksNeverGranted(t *testing.T) {
	skill := &pb.AgentSkill{
		Id:            "escalating-skill",
		AllowedScopes: []string{auth.ScopeContainersRead, auth.ScopeSecretsWrite},
	}
	ctx := auth.ContextWithTestSubjectScopes(
		context.Background(), "some-caller", nil, []string{auth.ScopeAgentsRun, auth.ScopeContainersRead})

	got := mintedAgentTokenScopes(ctx, skill)
	if slices.Contains(got, auth.ScopeSecretsWrite) {
		t.Fatalf("mintedAgentTokenScopes = %v; caller never held %q", got, auth.ScopeSecretsWrite)
	}
	if len(got) != 1 || got[0] != auth.ScopeContainersRead {
		t.Fatalf("mintedAgentTokenScopes = %v, want [%q]", got, auth.ScopeContainersRead)
	}
}

// TestMintedAgentTokenScopes_UnrestrictedCallerKeepsManifestUnchanged covers
// the Phase 1.7 backwards-compat path: a pre-scopes-claim caller (nil scopes)
// is unrestricted, so the manifest passes through as it did before #1676.
func TestMintedAgentTokenScopes_UnrestrictedCallerKeepsManifestUnchanged(t *testing.T) {
	skill := &pb.AgentSkill{Id: "hello-agent", AllowedScopes: []string{auth.ScopeContainersRead}}
	ctx := auth.ContextWithTestSubjectScopes(context.Background(), "some-caller", nil, nil)

	got := mintedAgentTokenScopes(ctx, skill)
	if len(got) != 1 || got[0] != auth.ScopeContainersRead {
		t.Fatalf("mintedAgentTokenScopes = %v, want manifest unchanged [%q]", got, auth.ScopeContainersRead)
	}
}

// TestMintedAgentTokenScopes_TrackerAdminNeverGranted is the fix for a
// gap caught in code review of #1924 (CWE-862): tracker:admin gates
// tracker connection CRUD, an operator decision that must never reach a
// bounded skill run — no matter how permissive the dispatching caller or
// the skill manifest's own allowed_scopes are. Covers both the
// unrestricted-caller path (IntersectScopes would otherwise pass the
// manifest through unchanged) and the explicit-caller-holds-it path.
func TestMintedAgentTokenScopes_TrackerAdminNeverGranted(t *testing.T) {
	skill := &pb.AgentSkill{
		Id:            "misconfigured-skill",
		AllowedScopes: []string{auth.ScopeContainersRead, auth.ScopeTrackerAdmin},
	}

	t.Run("unrestricted caller", func(t *testing.T) {
		ctx := auth.ContextWithTestSubjectScopes(context.Background(), "some-caller", nil, nil)
		got := mintedAgentTokenScopes(ctx, skill)
		if slices.Contains(got, auth.ScopeTrackerAdmin) {
			t.Fatalf("mintedAgentTokenScopes = %v, must never carry tracker:admin", got)
		}
		if !slices.Contains(got, auth.ScopeContainersRead) {
			t.Fatalf("mintedAgentTokenScopes = %v, want containers:read still granted (only tracker:admin is stripped)", got)
		}
	})

	t.Run("caller explicitly holds tracker:admin", func(t *testing.T) {
		ctx := auth.ContextWithTestSubjectScopes(context.Background(), "some-caller", nil,
			[]string{auth.ScopeContainersRead, auth.ScopeTrackerAdmin})
		got := mintedAgentTokenScopes(ctx, skill)
		if slices.Contains(got, auth.ScopeTrackerAdmin) {
			t.Fatalf("mintedAgentTokenScopes = %v, must never carry tracker:admin even when the caller holds it", got)
		}
	})
}

// TestMintedAgentTokenScopes_TokensDelegateNeverGranted is #2069: a run
// token holding tokens:delegate could call ExchangeDelegatedToken and mint a
// token with no run_id, which skips every run-token guard (#2060's lineage
// binding, #2112's taskRunID) because they all apply only `if runID != ""`.
// No shipped skill grants it, but a custom skill (CONTAINARIUM_SKILLS_DIR)
// could — so it is stripped at mint the same way tracker:admin is.
func TestMintedAgentTokenScopes_TokensDelegateNeverGranted(t *testing.T) {
	skill := &pb.AgentSkill{
		Id:            "custom-delegating-skill",
		AllowedScopes: []string{auth.ScopeContainersRead, auth.ScopeTokensDelegate},
	}

	t.Run("unrestricted caller", func(t *testing.T) {
		ctx := auth.ContextWithTestSubjectScopes(context.Background(), "some-caller", nil, nil)
		got := mintedAgentTokenScopes(ctx, skill)
		if slices.Contains(got, auth.ScopeTokensDelegate) {
			t.Fatalf("mintedAgentTokenScopes = %v, must never carry tokens:delegate", got)
		}
		if !slices.Contains(got, auth.ScopeContainersRead) {
			t.Fatalf("mintedAgentTokenScopes = %v, want containers:read still granted (only tokens:delegate is stripped)", got)
		}
	})

	t.Run("caller explicitly holds tokens:delegate", func(t *testing.T) {
		ctx := auth.ContextWithTestSubjectScopes(context.Background(), "some-caller", nil,
			[]string{auth.ScopeContainersRead, auth.ScopeTokensDelegate})
		got := mintedAgentTokenScopes(ctx, skill)
		if slices.Contains(got, auth.ScopeTokensDelegate) {
			t.Fatalf("mintedAgentTokenScopes = %v, must never carry tokens:delegate even when the caller holds it", got)
		}
	})
}

// TestMintedAgentTokenScopes_CodeAnswerNeverGranted is #3191: code:answer
// answers the run's own permission requests, so a skill run's in-box token
// must never carry it, whatever the caller or a custom manifest grants.
func TestMintedAgentTokenScopes_CodeAnswerNeverGranted(t *testing.T) {
	skill := &pb.AgentSkill{
		Id:            "custom-answering-skill",
		AllowedScopes: []string{auth.ScopeContainersRead, auth.ScopeCodeAnswer},
	}
	for name, callerScopes := range map[string][]string{
		"unrestricted caller":      nil,
		"caller holds code:answer": {auth.ScopeContainersRead, auth.ScopeCodeAnswer},
		"wildcard caller":          {auth.ScopeWildcard},
	} {
		ctx := auth.ContextWithTestSubjectScopes(context.Background(), "some-caller", nil, callerScopes)
		got := mintedAgentTokenScopes(ctx, skill)
		if slices.Contains(got, auth.ScopeCodeAnswer) {
			t.Errorf("%s: mintedAgentTokenScopes = %v, must never carry code:answer", name, got)
		}
		if !slices.Contains(got, auth.ScopeContainersRead) {
			t.Errorf("%s: mintedAgentTokenScopes = %v, want containers:read still granted", name, got)
		}
	}
}

// TestSendAgentTaskRequiresCallScope confirms the agents:call gate.
func TestSendAgentTaskRequiresCallScope(t *testing.T) {
	s := &AgentSkillServer{catalog: skills.GetDefault()}
	// Authenticated, but without agents:call.
	ctx := auth.ContextWithTestSubjectScopes(
		context.Background(), "agent-hello-agent", nil, []string{auth.ScopeAgentsRead})
	_, err := s.SendAgentTask(ctx, &pb.SendAgentTaskRequest{ToPeerId: "x"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied without agents:call, got %v", err)
	}
}

// TestMintedAgentAct_DerivedFromAuthenticatedCaller is the #1677 AC
// "RunAgentSkill populates it at mint": a single-hop caller (a human/CLI
// token with no act of its own) produces a depth-1 chain naming them.
func TestMintedAgentAct_DerivedFromAuthenticatedCaller(t *testing.T) {
	ctx := auth.ContextWithTestSubject(context.Background(), "alice", "user")
	got := mintedAgentAct(ctx)
	if got == nil {
		t.Fatal("mintedAgentAct = nil, want a chain naming the authenticated caller")
	}
	if got.Subject != "alice" {
		t.Errorf("got.Subject = %q, want alice", got.Subject)
	}
	if got.Act != nil {
		t.Errorf("got.Act = %+v, want nil (caller had no delegation of its own)", got.Act)
	}
}

// TestMintedAgentAct_NestsCallersOwnAct is the #1677 AC "a second hop nests
