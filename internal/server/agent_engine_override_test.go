package server

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/agentengine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRunAgentSkill_EngineOverride_RefusedWhenNotReady proves #2228's
// request-level override actually reaches agentengine.Resolve: hello-agent's
// own manifest names no engine, so with no override it would resolve via the
// gateway's default-provider path and never hit a readiness check at all
// (agentengine.Resolve's own UNSPECIFIED branch). A FailedPrecondition here
// can only be explained by the override being honored.
func TestRunAgentSkill_EngineOverride_RefusedWhenNotReady(t *testing.T) {
	store := newFakeRevocationStore()
	s, skill := newSkillBoxHarness(t, store) // skill = hello-agent, engine unspecified
	// Default gateway from the harness has no GlobalProviders/Keys at all, so
	// a NAMED engine (unlike UNSPECIFIED) is never ready here.
	ctx := ctxAs("admin", true)

	_, err := s.RunAgentSkill(ctx, &pb.RunAgentSkillRequest{
