package server

import (
	"context"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #2095: the in-box runtime journals every run under
// /var/log/agent-runtime/runs/<run_id>/<skill_id>.jsonl. These tests pin how
// the daemon hands it those two ids: the exec prefix for run mode, the A2A
// task for serve mode.

func TestRunModeCommand_ExportsRunAndSkillID(t *testing.T) {
	s := &AgentSkillServer{}
	// #2222: runModeCommand no longer derives the engine from s.gateway — the
	// caller (provisionSkillBoxWith) resolves it once via agentengine.Resolve
	// and passes the result in. This test pins rendering only.
	geminiResolved := agentengine.Resolved{Engine: pb.AgentEngine_AGENT_ENGINE_GEMINI, Provider: "gemini"}
	cases := []struct {
		name      string
		engineRes agentengine.Resolved
		seedDir   string
		runID     string
		skillID   string
		want      []string
