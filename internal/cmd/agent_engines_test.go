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
