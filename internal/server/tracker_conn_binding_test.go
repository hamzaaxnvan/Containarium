package server

import (
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---- enforceConnectionBinding: pure unit coverage, no store needed ----

func TestEnforceConnectionBinding_NoClaim_AllowsAnyConnection(t *testing.T) {
	// An operator/human token (no tracker_conn claim) may name any
	// connection in its tenant — design note decision D3.
	ctx := kmsKeyTestCtx("alice", "member", "tracker:read")
	if err := enforceConnectionBinding(ctx, "whatever"); err != nil {
		t.Errorf("operator token should be able to name any connection: %v", err)
	}
}

