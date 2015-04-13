package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
)

// The gate's staging scope is the AUTHENTICATED SUBJECT, not the deploy
// name (#2414). An admin may deploy a box under any name, so a name-derived
// scope would let an admin-token proxy that forwards a user-chosen name read
// any tenant's staged dataset. These tests pin the rule.

// moveStaged moves the fixture's staged dataset (staged under alice) to the
// same ref under owner, so only owner's staging directory holds it.
func moveStaged(t *testing.T, f *gateFixture, owner string) {
	t.Helper()
	dst := filepath.Join(f.srv.guardrailStagingRoot, owner, "ds1")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.staged, dst); err != nil {
		t.Fatal(err)
	}
	f.staged = dst
}

// TestDeployGate_AdminCannotReadAnotherTenantsStagingByName is #2414
// acceptance criterion 1: an admin caller naming tenant alice does not get
// alice's staged dataset, even with a valid attestation for it. The deploy
// is refused and no box is created.
func TestDeployGate_AdminCannotReadAnotherTenantsStagingByName(t *testing.T) {
	f := newGateFixture(t) // valid attestation over <root>/alice/ds1
	f.caller = adminWithScopes(auth.ScopeContainersWrite)
	f.req.Name = "alice"
	_, err := f.deploy(t)
