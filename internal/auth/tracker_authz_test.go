package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// #2268 — AuthorizeTrackerTenant: AuthorizeTenant plus the one extra way a
// RUN token passes, for the tenant its run_tenant claim names.

// runTokenCtx is the metadata shape a real run token produces after the
// HTTP middleware and gateway annotator: the BOX as subject, no roles, a
// run_id and the tenant the run was started for.
func runTokenCtx(runID, tenant string) context.Context {
	pairs := []string{MDKeyUsername, "agent-product-define", MDKeyRoles, ""}
	if runID != "" {
		pairs = append(pairs, MDKeyRunID, runID)
	}
	if tenant != "" {
		pairs = append(pairs, MDKeyRunTenant, tenant)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestAuthorizeTrackerTenant_RunTokenForItsOwnTenant(t *testing.T) {
	if err := AuthorizeTrackerTenant(runTokenCtx("run-1", "alice"), "alice"); err != nil {
		t.Fatalf("a run token started by alice must act on alice's tracker: %v", err)
	}
}

