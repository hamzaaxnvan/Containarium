package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---- A REAL run token on the tracker verbs (#2268) ---------------------
//
// Every other tracker verb test builds the run's context by hand with the
// TENANT as the subject (kmsKeyTestCtx(user, ...) + a run_id claim). A
// real run token is not minted that way: provisionSkillBoxWith mints it
// for the BOX's subject (agent-<skill-id>, no roles) with the dispatching
// tenant only in its `act` audit claim — see mintRunToken. The tracker
// verbs then authorize req.Username (the tenant, from the dispatch input)
// against the token's subject. These tests put the two together with a
// token from the real mint path, through the real HTTP auth middleware,
// so the hand-built shortcut can no longer hide a mismatch between what
// is minted and what the verbs check.

// realRunTokenMinter is the minimal AgentSkillServer the mint path needs:
// the token manager and the catalog. Nothing here touches a box.
func realRunTokenMinter(t *testing.T) (*AgentSkillServer, *auth.TokenManager, *pb.AgentSkill) {
	t.Helper()
	tm, err := auth.NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	catalog := skills.GetDefault()
	skill, err := catalog.Get("product-define")
	if err != nil {
		t.Fatalf("product-define not in the catalog: %v", err)
	}
	return &AgentSkillServer{catalog: catalog, tokens: tm}, tm, skill
}

// dispatchCallerCtx is the context the tracker dispatcher starts a run
// under: the tenant's OWN token (requireDispatchCaller), which must also
// pass provisionBoxOnly's AuthorizeTenant on the agent box — so, in
// production, an admin-role token for the tenant. No scopes claim, so the
// run receives exactly the skill manifest's scopes (#1676).
func dispatchCallerCtx(user string) context.Context {
	return kmsKeyTestCtx(user, auth.RoleAdmin, "")
}

// mintRealRunToken mints a run token for `user`, bound to connection
// `conn`, through the same code provisionSkillBoxWith runs for a real
// dispatched run, and returns the raw JWT.
func mintRealRunToken(t *testing.T, user, runID, conn string) (string, *auth.TokenManager) {
	t.Helper()
	agents, tm, skill := realRunTokenMinter(t)
	tok, cred, err := agents.mintRunToken(dispatchCallerCtx(user), skill, runID, conn)
	if err != nil {
		t.Fatalf("mintRunToken: %v", err)
	}
	if cred.Kind != runlease.KindPlatformJWT || cred.JTI == "" {
		t.Fatalf("lease credential = %+v, want the platform JWT with its jti", cred)
	}
	return tok, tm
