package server

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/crews"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func modelTestSkills() (*pb.Crew, func(string) (*pb.AgentSkill, bool)) {
	byID := map[string]*pb.AgentSkill{
		"named-pinned": {Id: "named-pinned", Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE, Model: "pinned-model"},
		"named-bare":   {Id: "named-bare", Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE},
		"no-engine":    {Id: "no-engine"},
	}
	crew := &pb.Crew{Id: "c", SkillIds: []string{"named-pinned", "named-bare", "no-engine"}}
	return crew, func(id string) (*pb.AgentSkill, bool) { s, ok := byID[id]; return s, ok }
}

func TestValidateCrewModelOverrides(t *testing.T) {
