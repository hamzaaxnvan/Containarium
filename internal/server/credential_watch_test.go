package server

import (
	"context"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/alert"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The daemon side of the credential-expiry watch (#2371): AgentSkillServer
// is the watcher's CredentialProber.
var _ alert.CredentialProber = (*AgentSkillServer)(nil)

// TestGetSkillBoxCredentialStatus_ReportsExpired: the probe's new "expired"
// answer surfaces as the new wire enum value, not UNSPECIFIED or an error.
func TestGetSkillBoxCredentialStatus_ReportsExpired(t *testing.T) {
	s, skill, backend := newSkillBoxHarnessInspectable(t, newFakeRevocationStore())
	backend.execStdout = "expired\n"

	resp, err := s.GetSkillBoxCredentialStatus(ctxAs("admin", true), &pb.GetSkillBoxCredentialStatusRequest{SkillId: skill.Id})
	if err != nil {
		t.Fatalf("GetSkillBoxCredentialStatus: %v", err)
	}
	if resp.GetCredentialSource() != pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_EXPIRED {
		t.Errorf("CredentialSource = %v, want EXPIRED", resp.GetCredentialSource())
	}
}

// TestCredentialWatchTargets_OnlyRunningProbeableSkillBoxes: the watcher
// sweeps a skill's box only once it is running (a stopped box cannot be
// probed) and only for an engine that has a probe — a codex-pinned skill
// is left out rather than probed with the wrong script.
func TestCredentialWatchTargets_OnlyRunningProbeableSkillBoxes(t *testing.T) {
	s, skills, backend := newCredentialWatchHarness(t, "hello-agent", "hello-agent-codex")
	ctx := context.Background()

	got, err := s.CredentialWatchTargets(ctx)
	if err != nil {
		t.Fatalf("CredentialWatchTargets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("targets with every box stopped = %+v, want none", got)
	}

	for _, sk := range skills {
		if err := backend.StartContainer("agent-" + sk.Id + "-container"); err != nil {
			t.Fatalf("start %s: %v", sk.Id, err)
		}
	}
	got, err = s.CredentialWatchTargets(ctx)
	if err != nil {
		t.Fatalf("CredentialWatchTargets: %v", err)
	}
	want := alert.CredentialWatchTarget{SkillID: "hello-agent", Box: "agent-hello-agent-container", Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("targets = %+v, want exactly [%+v]", got, want)
	}
}

// The alert policy differs from raw status, but uses the shared executor.
func TestProbeCredential_UsesAlertPolicy(t *testing.T) {
	s, skill, backend := newSkillBoxHarnessInspectable(t, newFakeRevocationStore())
	backend.execStdout = "expired\n"

	src, err := s.ProbeCredential(context.Background(), alert.CredentialWatchTarget{
		SkillID: skill.Id, Box: "agent-" + skill.Id + "-container", Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE,
	})
	if err != nil {
		t.Fatalf("ProbeCredential: %v", err)
	}
	if src != pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_EXPIRED {
		t.Errorf("source = %v, want EXPIRED", src)
	}
	if len(backend.execCalls) != 1 || backend.execCalls[0].ContainerName != "agent-"+skill.Id+"-container" {
		t.Errorf("exec calls = %+v, want exactly one probe in the skill's box", backend.execCalls)
	}

	if got := backend.execCalls[0].Command[2]; !strings.Contains(got, "86400") {
		t.Fatal("watcher did not execute the alert policy")
	}
