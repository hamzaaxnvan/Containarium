//go:build integration

// Ownership of a shared key's ssh_keys row (#3217): an owner's drop while
// another account still holds the key releases the row instead of leaving the
// dropper as owner of a key it no longer holds, a later holder can take a
// released row over, and ownership compares user_id for an owner account.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/sshauthz/ -run Ownership
package sshauthz

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// heldKeys is the store with the holder lookup reading homeRoot.
func heldKeys(t *testing.T, homeRoot string) *PGKeyStore {
	t.Helper()
	return pgKeysHeld(t, homeRoot)
}

func dropHeld(t *testing.T, homeRoot, user string) {
	t.Helper()
	if err := os.Remove(filepath.Join(homeRoot, user, ".ssh", "authorized_keys")); err != nil {
		t.Fatal(err)
	}
}

// aliceFirst registers pub to alice and then to bob, both holding it, so
// first registration leaves alice as owner. It returns the fingerprint.
func aliceFirst(t *testing.T, s *PGKeyStore, home, pub string) string {
	t.Helper()
	ctx := context.Background()
	rec := mustRecord(t, pub, "alice", SourceAPI, caseNow)
	for _, r := range []KeyRecord{rec, mustRecord(t, pub, "bob", SourceAPI, caseNow.Add(time.Minute))} {
		if err := s.Register(ctx, r); err != nil {
			t.Fatal(err)
		}
		holdKey(t, home, r.Username, pub)
	}
	if got := lookup(t, s, rec.Fingerprint); got.Username != "alice" || !got.RevokedAt.IsZero() {
		t.Fatalf("precondition: row = %+v, want active and owned by alice", got)
	}
	return rec.Fingerprint
}
