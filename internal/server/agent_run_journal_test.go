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
		wantOrder []string
	}{
		{
			name: "direct mode", seedDir: "/seed/run-1", runID: "run-1", skillID: "hello-agent",
			want: []string{
				"CONTAINARIUM_RUN_ID='run-1' ",
				"CONTAINARIUM_SKILL_ID='hello-agent' ",
				"AGENT_SEED_DIR=/seed/run-1 agent-runtime",
				sourceGatewayEnvPrefix("/seed/run-1"),
			},
		},
		{
			name: "gateway mode keeps the engine pin", engineRes: geminiResolved, seedDir: "/seed/r2", runID: "r2", skillID: "relay-agent",
			want: []string{
				"CONTAINARIUM_AGENT_ENGINE=gemini ",
				"CONTAINARIUM_RUN_ID='r2' ",
				"CONTAINARIUM_SKILL_ID='relay-agent' ",
			},
			// The gateway env is sourced first, so a gateway.env can never
			// shadow the run id the daemon exports for this exec.
			wantOrder: []string{"gateway.env", "CONTAINARIUM_RUN_ID=", "agent-runtime"},
		},
		{
			name: "ids are shell-quoted", seedDir: "/seed/x", runID: "a.b_c-1", skillID: "it's",
			want: []string{"CONTAINARIUM_RUN_ID='a.b_c-1' ", `CONTAINARIUM_SKILL_ID='it'\''s' `},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.runModeCommand(tc.seedDir, tc.runID, tc.skillID, tc.engineRes, "")
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("runModeCommand = %q, want it to contain %q", got, w)
