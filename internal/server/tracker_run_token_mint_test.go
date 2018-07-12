package server

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestMintRunToken_CarriesTenantRunAndConnection pins what the real run
// token says (#2268), with no Postgres: the subject is the BOX, the tenant
// the run was started for is the run_tenant claim (derived from the
// dispatching caller's verified subject, the same identity the connection
// was validated under), alongside run_id, tracker_conn, the act audit
// chain, and exactly the manifest's scopes.
func TestMintRunToken_CarriesTenantRunAndConnection(t *testing.T) {
	const user = "tracker-real-run-token-claims"
	agents, tm, skill := realRunTokenMinter(t)
	tok, _, err := agents.mintRunToken(dispatchCallerCtx(user), skill, "run-claims", "default")
	if err != nil {
		t.Fatalf("mintRunToken: %v", err)
	}
	claims, err := tm.ValidateAccessToken(tok)
	if err != nil {
		t.Fatalf("ValidateAccessToken: %v", err)
	}
	if claims.Username != agentBoxPrefix+skill.Id {
		t.Errorf("subject = %q, want the box %q", claims.Username, agentBoxPrefix+skill.Id)
	}
	if len(claims.Roles) != 0 {
		t.Errorf("roles = %v, want none (a run token must never carry a role)", claims.Roles)
	}
	if claims.RunTenant != user {
		t.Errorf("run_tenant = %q, want the dispatching tenant %q", claims.RunTenant, user)
	}
	if claims.RunID != "run-claims" || claims.TrackerConn != "default" {
		t.Errorf("run_id/tracker_conn = %q/%q, want run-claims/default", claims.RunID, claims.TrackerConn)
	}
	if claims.Act == nil || claims.Act.Subject != user {
		t.Errorf("act = %+v, want the dispatching tenant as the delegation subject", claims.Act)
	}
