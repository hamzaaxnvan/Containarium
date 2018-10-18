package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #1716 and #1717: two write paths that reached real state with no
// authorization guard at all. These assert the denial, not the happy path —
// the happy path was never broken, and a test that only proves the guard lets
// the right caller through would still pass with the guard removed.

// scopedTenantCtx is tenantCtx (rbac_phase_1_4_tenant_test.go) plus a scopes
// claim — these guards check scope AND tenancy, so both have to be set.
func scopedTenantCtx(username string, scopes ...string) context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), username, []string{"user"}, scopes)
}

// #1716. The username is request input naming whose box gets touched. Before
