package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/internal/sshauthz"
	"github.com/footprintai/containarium/pkg/core/container"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Done-when 4 of #2517: every key write path records the key in the
// ssh_keys registry, and RemoveSSHKey revokes it. The registry's own
// semantics (first registration wins, revoke only the owner's row) are tested
// against Postgres in internal/sshauthz; here a recording fake proves each
// path hands it the right row.

type revocation struct {
	rec sshauthz.KeyRecord
	at  time.Time
}

type recordingRegistry struct {
	registered []sshauthz.KeyRecord
	revoked    []revocation
	err        error
}

func (r *recordingRegistry) Register(_ context.Context, rec sshauthz.KeyRecord) error {
	if r.err != nil {
		return r.err
	}
	r.registered = append(r.registered, rec)
	return nil
}

func (r *recordingRegistry) Revoke(_ context.Context, rec sshauthz.KeyRecord, at time.Time) error {
	if r.err != nil {
		return r.err
	}
	r.revoked = append(r.revoked, revocation{rec, at})
	return nil
}

func newTestPubKey(t *testing.T) string {
	t.Helper()
	return newTestPubKeyFrom(t, func() ed25519.PublicKey {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return pub
	})
}

// newTestPubKeyFrom draws public keys from next until one passes the
// production validator. A random base64 body can contain a placeholder marker
// (e.g. "todo") and be rejected by SetSSHKeys, so such draws are discarded.
// It asks the validator rather than keeping its own marker list.
func newTestPubKeyFrom(t *testing.T, next func() ed25519.PublicKey) string {
	t.Helper()
	for i := 0; i < 1000; i++ {
		line := testPubKeyLine(t, next())
		if container.ValidateSSHPublicKey(line) == nil {
			return line
		}
	}
	t.Fatal("newTestPubKeyFrom: no validator-clean key after 1000 draws")
	return ""
}

func testPubKeyLine(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
}

// A draw whose encoding contains a placeholder marker must be regenerated.
func TestNewTestPubKey_RegeneratesPlaceholderMarkerDraw(t *testing.T) {
	// The wire blob has a 19-byte header (len+"ssh-ed25519"+len), so key
	// byte 2 starts a base64 group (offset 21 = 7*3). Put the 3 bytes that
	// encode "todo" there.
	marker, err := base64.StdEncoding.DecodeString("todo")
	if err != nil {
		t.Fatal(err)
	}
	bad := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(bad[2:], marker)
	if container.ValidateSSHPublicKey(testPubKeyLine(t, bad)) == nil {
		t.Fatal("precondition: forced key should be rejected by the validator")
	}
	good, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	draws := 0
	got := newTestPubKeyFrom(t, func() ed25519.PublicKey {
		draws++
		if draws == 1 {
			return bad
		}
		return good
	})
	if draws < 2 {
		t.Fatalf("placeholder-marker draw was returned without regeneration (draws=%d)", draws)
	}
	if err := container.ValidateSSHPublicKey(got); err != nil {
		t.Fatalf("newTestPubKeyFrom returned a key the validator rejects: %v", err)
	}
}

// fakeAuthorizedKeys replaces the host-side authorized_keys file operations.
type fakeAuthorizedKeys struct {
	files  map[string][]string
	addErr map[string]error // by key

	platform     string // platform-managed line the fake replace preserves
	inReplace    bool   // true while the fake replace's critical section is open
	replaceCalls int
}

func newFakeAuthorizedKeys() *fakeAuthorizedKeys {
	return &fakeAuthorizedKeys{files: map[string][]string{}, addErr: map[string]error{}}
}

// OtherAccountHolds is the fake's sshauthz.HolderLookup: any account other
// than excludeUsername whose fake file holds the fingerprint.
func (f *fakeAuthorizedKeys) OtherAccountHolds(_ context.Context, fingerprint, excludeUsername string) (bool, error) {
	for user, lines := range f.files {
		if user == excludeUsername {
			continue
		}
		for _, l := range lines {
			if rec, err := sshauthz.NewKeyRecord(l, user, sshauthz.SourceAPI, caseClock); err == nil && rec.Fingerprint == fingerprint {
				return true, nil
			}
		}
	}
	return false, nil
}

func (f *fakeAuthorizedKeys) ops() authorizedKeysOps {
	return authorizedKeysOps{
		add: func(u, k string) error {
			if err := f.addErr[k]; err != nil {
				return err
			}
			f.files[u] = append(f.files[u], k)
			return nil
		},
		remove: func(u, k string) error {
			var kept []string
			for _, l := range f.files[u] {
				if l != k {
					kept = append(kept, l)
				}
			}
			f.files[u] = kept
			return nil
		},
