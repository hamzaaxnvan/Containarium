package server

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestProvisionSkillBox_ReusesExistingBoxWithoutMintingOrSeeding is #2272's
// core acceptance criterion: a skill's box can be provisioned ahead of any
// real run, and that provisioning never mints a token, never seeds a
// prompt/task, and never execs anything in the box — RunAgentSkill's seed
// exec is the only thing a model call could ever ride in on, and this path
// must not go near it.
//
// newSkillBoxHarness pre-creates the box in "Stopped" state (the fake
// backend's CreateContainer default), which exercises the REUSE branch:
// ProvisionSkillBox must start it (so a human can SSH in) without deploying
// a new one or touching exec.
func TestProvisionSkillBox_ReusesExistingBoxWithoutMintingOrSeeding(t *testing.T) {
	store := newFakeRevocationStore()
	s, skill, backend := newSkillBoxHarnessInspectable(t, store)

	resp, err := s.ProvisionSkillBox(ctxAs("admin", true), &pb.ProvisionSkillBoxRequest{SkillId: skill.Id})
	if err != nil {
		t.Fatalf("ProvisionSkillBox: %v", err)
	}
	if resp.GetFreshlyProvisioned() {
		t.Error("FreshlyProvisioned = true, want false — the harness pre-creates the box, this must be the reuse path")
	}
	if resp.GetContainer() == nil {
		t.Fatal("Container is nil")
	}
	if resp.GetContainer().GetState() != pb.ContainerState_CONTAINER_STATE_RUNNING {
		t.Errorf("Container.State = %v, want RUNNING (a stopped box must be started so a human can SSH in)", resp.GetContainer().GetState())
	}
	if len(backend.execCalls) != 0 {
		t.Errorf("ProvisionSkillBox must never exec in the box (no seed, no run); got exec calls: %+v", backend.execCalls)
	}
}

// TestProvisionSkillBox_RequiresSkillID pins the InvalidArgument guard.
func TestProvisionSkillBox_RequiresSkillID(t *testing.T) {
	s := &AgentSkillServer{catalog: skills.GetDefault()}
	_, err := s.ProvisionSkillBox(ctxAs("admin", true), &pb.ProvisionSkillBoxRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
}

// TestProvisionSkillBox_UnknownSkillIsNotFound pins that an unregistered
// skill id fails NotFound before any box work is attempted.
func TestProvisionSkillBox_UnknownSkillIsNotFound(t *testing.T) {
	store := newFakeRevocationStore()
	s, _ := newSkillBoxHarness(t, store)

	_, err := s.ProvisionSkillBox(ctxAs("admin", true), &pb.ProvisionSkillBoxRequest{SkillId: "no-such-skill"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

// TestGetSkillBoxCredentialStatus_RequiresProvisionedBox: the credential
// check is only meaningful once ProvisionSkillBox (or a run) has created the
// box. An unprovisioned skill must fail NotFound, naming the fix.
func TestGetSkillBoxCredentialStatus_RequiresProvisionedBox(t *testing.T) {
	tm, err := auth.NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	backend := newFakeSandboxBackend()
	cs := &ContainerServer{manager: container.NewWithBackend(backend)}
	s := &AgentSkillServer{catalog: skills.GetDefault(), recipes: NewRecipeServer(cs, nil), tokens: tm}

	_, err = s.GetSkillBoxCredentialStatus(ctxAs("admin", true), &pb.GetSkillBoxCredentialStatusRequest{SkillId: "hello-agent"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound (box never provisioned)", status.Code(err))
	}
}

// TestGetSkillBoxCredentialStatus_ReportsProbeSourceNameOnly proves the
// RPC surfaces exactly the probe's source NAME and nothing else: it stuffs
// an arbitrary (non-credential-shaped) string as the fake exec's stdout and
// asserts the typed enum round-trips it — there is no value field anywhere
// on the response for a real credential to leak through even if the box's
// probe somehow printed one.
func TestGetSkillBoxCredentialStatus_ReportsProbeSourceNameOnly(t *testing.T) {
	store := newFakeRevocationStore()
	s, skill, backend := newSkillBoxHarnessInspectable(t, store)
