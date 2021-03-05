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
	crew, get := modelTestSkills()
	for _, tc := range []struct {
		name string
		req  *pb.RunCrewRequest
		want codes.Code
	}{
		{"none is fine", &pb.RunCrewRequest{}, codes.OK},
		{"a named-engine member", &pb.RunCrewRequest{ModelOverrides: map[string]string{"named-bare": "m-1"}}, codes.OK},
		{"a member the crew does not have", &pb.RunCrewRequest{ModelOverrides: map[string]string{"stranger": "m-1"}}, codes.InvalidArgument},
		{"a malformed id", &pb.RunCrewRequest{ModelOverrides: map[string]string{"named-bare": "x; rm -rf /"}}, codes.InvalidArgument},
		{"an empty id", &pb.RunCrewRequest{ModelOverrides: map[string]string{"named-bare": ""}}, codes.InvalidArgument},
		// A model is only carried to the box together with a named engine; on a
		// default-engine member it would be dropped silently, so it is refused.
		{"a member with no engine named", &pb.RunCrewRequest{ModelOverrides: map[string]string{"no-engine": "m-1"}}, codes.FailedPrecondition},
		{"no engine in the manifest but one in engine_overrides", &pb.RunCrewRequest{
			ModelOverrides:  map[string]string{"no-engine": "m-1"},
			EngineOverrides: map[string]pb.AgentEngine{"no-engine": pb.AgentEngine_AGENT_ENGINE_CODEX}}, codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateModelOverrides("crew", crew.GetSkillIds(), tc.req, get)
			if status.Code(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.want)
			}
		})
