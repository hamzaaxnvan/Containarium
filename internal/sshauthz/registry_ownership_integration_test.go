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

// The residual gap #3226 left: alice (owner) drops a key bob still holds, so
// her revoke is refused. She must not stay owner of a key she no longer
// holds, or bob's later drop is a non-owner no-op and the row stays active
// with no holder at all. Her drop releases the row, and bob's drop as the
// sole holder then revokes it.
func TestPGKeyStoreOwnership_OwnerDropReleasesSharedRow(t *testing.T) {
	home := t.TempDir()
	s := heldKeys(t, home)
	ctx := context.Background()
	pub := testKey(t)
	fp := aliceFirst(t, s, home, pub)

	at := caseNow.Add(time.Hour)
	if err := s.Revoke(ctx, mustRecord(t, pub, "alice", SourceAPI, at), at); !errors.Is(err, ErrKeyHeldByOtherAccount) {
		t.Fatalf("alice's revoke = %v, want ErrKeyHeldByOtherAccount", err)
	}
	dropHeld(t, home, "alice")
	if got := lookup(t, s, fp); !got.RevokedAt.IsZero() {
		t.Fatalf("alice's drop revoked a key bob holds: %+v", got)
	}

	if err := s.Revoke(ctx, mustRecord(t, pub, "bob", SourceAPI, at), at); err != nil {
		t.Fatalf("bob's revoke as the sole holder: %v", err)
	}
	if got := lookup(t, s, fp); got.RevokedAt.IsZero() {
		t.Fatalf("bob dropped the key as its sole holder and the row stayed active: %+v", got)
	}
}

// A released row is taken over by the next registration: the holder that
// re-adds the key becomes the owner. Once owned again, a non-owner's revoke
// changes nothing.
func TestPGKeyStoreOwnership_RegisterTakesOverReleasedRow(t *testing.T) {
	home := t.TempDir()
	s := heldKeys(t, home)
	ctx := context.Background()
	pub := testKey(t)
	fp := aliceFirst(t, s, home, pub)

	at := caseNow.Add(time.Hour)
	if err := s.Revoke(ctx, mustRecord(t, pub, "alice", SourceAPI, at), at); !errors.Is(err, ErrKeyHeldByOtherAccount) {
		t.Fatalf("alice's revoke = %v, want ErrKeyHeldByOtherAccount", err)
	}
	dropHeld(t, home, "alice")

	if err := s.Register(ctx, mustRecord(t, pub, "bob", SourceAPI, at.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
