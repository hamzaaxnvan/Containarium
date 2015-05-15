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
