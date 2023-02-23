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
