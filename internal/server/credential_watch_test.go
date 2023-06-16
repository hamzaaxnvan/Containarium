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
