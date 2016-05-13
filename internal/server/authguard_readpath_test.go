package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #1718: nine read-only RPCs returned static or platform-wide config with no
// authorization call at all. None are per-tenant, so the fix is a read scope
// rather than a tenant check — but "close to public already" is not the same
// as "reachable with no token authority", which is what they were.
//
// The reconnaissance value is the point: an unauthenticated caller could learn
// which security tooling is enabled, whether an alerting webhook is
// configured, and what the threat blocklist watches for.

// wrongScopeCtx is authenticated and genuinely scoped — it just holds the
// wrong scope. That is the realistic "least-privilege token without this
// authority" case, and the one these guards must deny.
//
// NOT an empty scope set: an empty claim currently collapses to the same
// (nil, false) as an absent one and is therefore treated as UNRESTRICTED, so a
// test written that way would pass whether or not the guard existed. That
// collapse contradicts ScopesFromGRPCContext's own doc comment and is filed
// separately; it is not what #1718 is about.
func wrongScopeCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "nobody",
		[]string{"user"}, []string{auth.ScopeBackupsRead})
}

func TestReadOnlyRPCs_RequireSomeReadAuthority(t *testing.T) {
	cs := &ContainerServer{}
	td := &ThreatDetectionServer{}

	cases := []struct {
		name string
		call func(context.Context) error
	}{
