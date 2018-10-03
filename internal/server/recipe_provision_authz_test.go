package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #2504: agents:run "provisions a skill's box and runs a task" (scopes.go),
// but the recipe path's inner create went through the public CreateContainer
// RPC, which re-checked the CALLER for containers:write — so a token holding
// only its documented scope failed the first run of every skill on a daemon.
// The inner create now runs under the daemon's own authority once the entry
// point has authorised the caller; the public RPC and DeployRecipe's own
// entry check are unchanged, and deploy refuses an unauthenticated context.
//
// Same posture as scope_gate_test.go: servers are built with nil deps, so a
// call that clears the gate panics further down — mustPassScope treats that
// as proof the gate is structural.

// agentsRunOnly is the shape of a tracker-dispatch or skill-run credential:
// admin role (AuthorizeTenant on the box name passes), a scopes claim
// present, agents:run and nothing about containers.
func agentsRunOnly(name string) context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), name,
		[]string{"admin"}, []string{auth.ScopeAgentsRun})
}

// The seam deploy provisions through must not repeat the caller's scope
// check. Before the fix this returned PermissionDenied "scope required:
// containers:write".
func TestRecipeInnerCreate_DoesNotRecheckCallerForContainersWrite(t *testing.T) {
