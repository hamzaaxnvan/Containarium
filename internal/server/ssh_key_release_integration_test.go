//go:build integration

// Shared-key ownership through the RPCs (#3217), over the real registry:
// an owner's drop of a key another account still holds releases the row, so
// the remaining holder's own drop revokes it; no other tenant can cut the
// remaining holder off; and ownership compares user_id for an owner account.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/server/ -run SharedKey
package server

import (
	"context"
	"slices"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sshauthz"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// aliceFirstShared has alice add bob's public key before bob does, so alice
// owns the row, and bob then adds it to his own box.
func aliceFirstShared(t *testing.T) (*ContainerServer, *sshauthz.PGKeyStore, *fakeAuthorizedKeys, string) {
	t.Helper()
	s, keys, fs := crossTenantServer(t)
	bobKey := newTestPubKey(t)
	for _, u := range []string{"alice", "bob"} {
		if _, err := s.AddSSHKey(tenantWithScopes(u, auth.ScopeSSHWrite), &pb.AddSSHKeyRequest{Username: u, SshPublicKey: bobKey}); err != nil {
			t.Fatalf("%s AddSSHKey: %v", u, err)
		}
	}
	if owner, active := keyState(t, keys, bobKey); owner != "alice" || !active {
		t.Fatalf("precondition: row = owner %q active %v, want alice, active", owner, active)
	}
	return s, keys, fs, bobKey
}

func removeKey(t *testing.T, s *ContainerServer, user, pub string) {
	t.Helper()
	if _, err := s.RemoveSSHKey(tenantWithScopes(user, auth.ScopeSSHWrite), &pb.RemoveSSHKeyRequest{Username: user, SshPublicKey: pub}); err != nil {
		t.Fatalf("%s RemoveSSHKey: %v", user, err)
	}
}

// The residual gap #3226 left: alice's drop is skipped for the registry
// because bob holds the key, and bob's own later drop must then revoke it
// rather than leave an active row nobody holds.
func TestSSHKeys_SharedKeyHolderDropRevokesAfterOwnerDrop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		aliceDrop func(t *testing.T, s *ContainerServer, pub string)
	}{
		{"RemoveSSHKey", func(t *testing.T, s *ContainerServer, pub string) { removeKey(t, s, "alice", pub) }},
		{"SetSSHKeys allow_empty", func(t *testing.T, s *ContainerServer, _ string) {
			if _, err := s.SetSSHKeys(tenantWithScopes("alice", auth.ScopeSSHWrite), &pb.SetSSHKeysRequest{Username: "alice", AllowEmpty: true}); err != nil {
				t.Fatalf("alice SetSSHKeys: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, keys, fs, bobKey := aliceFirstShared(t)
			tc.aliceDrop(t, s, bobKey)
			if _, active := keyState(t, keys, bobKey); !active {
				t.Fatal("alice's drop revoked a key bob holds")
			}

			removeKey(t, s, "bob", bobKey)
			if slices.Contains(fs.files["bob"], bobKey) {
				t.Fatal("bob's authorized_keys still holds the key")
			}
			if _, active := keyState(t, keys, bobKey); active {
				t.Fatal("bob dropped the key as its sole holder and the registry row stayed active")
			}
		})
	}
