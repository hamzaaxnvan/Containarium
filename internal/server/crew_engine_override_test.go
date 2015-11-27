package server

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/pkg/core/crews"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRunCrew_EngineOverrides_AppliedPerMember reuses #2225's own embedded
// fixture (two-engine-crew: hello-agent-claude then hello-agent-codex) to
// prove #2228's per-member override reaches provisionMemberBox. The
// harness's gateway marks only openai as a ready global provider, so without
// an override hello-agent-claude's own manifest engine (claude/anthropic)
// is refused; with an override to codex it is not.
func TestRunCrew_EngineOverrides_AppliedPerMember(t *testing.T) {
	store := newFakeRevocationStore()
	agents, _ := newSkillBoxHarnessForSkills(t, store, "hello-agent-claude", "hello-agent-codex")
	agents.gateway = &gatewayProvisioning{
		engines: agentengine.Gateway{
			DefaultProvider: "anthropic",
