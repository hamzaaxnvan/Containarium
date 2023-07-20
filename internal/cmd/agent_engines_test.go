package cmd

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func fixtureEnginesResponse() *pb.ListAgentEnginesResponse {
	return &pb.ListAgentEnginesResponse{
		KeyOwner: "user:alice",
		Engines: []*pb.AgentEngineStatus{
			{
				Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE, Provider: pb.GatewayProvider_GATEWAY_PROVIDER_ANTHROPIC,
				Readiness: pb.AgentEngineReadiness_AGENT_ENGINE_READINESS_READY,
				Source:    pb.AgentCredentialSource_AGENT_CREDENTIAL_SOURCE_GLOBAL_KEY,
				IsDefault: true, SkillIds: []string{"hello-agent"},
			},
			{
				Engine: pb.AgentEngine_AGENT_ENGINE_CODEX, Provider: pb.GatewayProvider_GATEWAY_PROVIDER_OPENAI,
				Readiness: pb.AgentEngineReadiness_AGENT_ENGINE_READINESS_NOT_READY,
				Reason:    "no key for provider openai; fix: containarium gateway key set user:alice --provider openai --key <value>",
			},
			{
				Engine: pb.AgentEngine_AGENT_ENGINE_GEMINI, Provider: pb.GatewayProvider_GATEWAY_PROVIDER_GEMINI,
				Readiness: pb.AgentEngineReadiness_AGENT_ENGINE_READINESS_UNKNOWN_DIRECT_MODE,
				Source:    pb.AgentCredentialSource_AGENT_CREDENTIAL_SOURCE_DIRECT_MODE,
				Reason:    "daemon serves no model gateway; the box's own secrets decide",
			},
		},
	}
}

// TestRenderAgentEngines_Table pins the CLI's human-readable table. Per the
// issue, the reason text must be printed byte-for-byte what the daemon sent —
