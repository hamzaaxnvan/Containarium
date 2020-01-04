package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/footprintai/containarium/pkg/core/secrets"
)

// --- #2402: key-wrap seam and managed key mode ---
//
// With a wrapper (a secrets.KMSClient) configured, Create generates an
// ephemeral age identity per backup, encrypts the dump to it (and to the
// legacy tenant recipient when one is given), wraps the identity's secret
// string with the KMS and records wrapped_key / kek_id / key_mode. The
// daemon never unwraps: restore and verify keep taking the identity from
// the caller, who unwrapped it under their own credentials.

// testWrapper is an InProcKMS over a fixed master key, plus a recording
// seam so a test can assert what was wrapped and make Wrap fail.
type testWrapper struct {
	*secrets.InProcKMS
	failWrap   error
	wrapCalls  int
	lastSecret []byte // copy of the plaintext handed to Wrap
}

func newTestWrapper(t *testing.T) *testWrapper {
	t.Helper()
	k, err := secrets.NewInProcKMS(bytes.Repeat([]byte{0x42}, secrets.MasterKeySize))
	if err != nil {
		t.Fatal(err)
	}
	return &testWrapper{InProcKMS: k}
}

func (w *testWrapper) Wrap(ctx context.Context, plaintext []byte) ([]byte, string, error) {
	w.wrapCalls++
	w.lastSecret = append([]byte(nil), plaintext...)
	if w.failWrap != nil {
		return nil, "", w.failWrap
	}
	return w.InProcKMS.Wrap(ctx, plaintext)
}

func newManagedManager(t *testing.T, ops ContainerOps, up Uploader, w secrets.KMSClient) *Manager {
	t.Helper()
	m := NewManager(ops, up, t.TempDir(), WithWrapper(w))
	m.clock = fixedClock
	return m
}

// unwrapIdentity is what the operator / cloud verifier does out of band:
// Unwrap the record's wrapped_key under their own credentials and get the
// age identity string back.
func unwrapIdentity(t *testing.T, w *testWrapper, rec *Record) string {
	t.Helper()
	pt, err := w.Unwrap(context.Background(), rec.WrappedKey, rec.KEKID)
	if err != nil {
		t.Fatalf("Unwrap(wrapped_key, kek_id=%q): %v", rec.KEKID, err)
	}
	if _, err := age.ParseX25519Identity(string(pt)); err != nil {
		t.Fatalf("unwrapped bytes are not an age identity: %v", err)
	}
	return string(pt)
}

// recipientStanzas counts the X25519 recipient stanzas in an age header —
// one per recipient the file was encrypted to.
func recipientStanzas(ciphertext []byte) int {
	head := ciphertext
	if i := bytes.Index(head, []byte("\n---")); i >= 0 {
		head = head[:i]
	}
	return bytes.Count(head, []byte("-> X25519 "))
}

// dirContains reports whether any regular file under dir contains needle.
func dirContains(t *testing.T, dir string, needle []byte) bool {
	t.Helper()
	found := false
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(path) // #nosec G304 -- test temp dir
		if err != nil {
			return err
		}
		if bytes.Contains(b, needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestCreate_ManagedWrapsEphemeralIdentity(t *testing.T) {
	payload := []byte("PGDMP\x00plaintext-archive-bytes")
	w := newTestWrapper(t)
	m := newManagedManager(t, newFakeOps(payload), nil, w)

	rec, err := m.Create(CreateOptions{
		Username: "alice", ContainerName: "alice-container",
		Conn: PgConn{Database: "app"}, Destination: DestLocal,
	})
	if err != nil {
		t.Fatalf("Create (managed): %v", err)
	}

	// Record shape.
	if rec.KeyMode != KeyModeManaged {
		t.Errorf("KeyMode = %q, want %q", rec.KeyMode, KeyModeManaged)
	}
	if len(rec.WrappedKey) == 0 {
		t.Error("WrappedKey is empty")
	}
	if rec.KEKID == "" {
		t.Error("KEKID is empty")
	}
	if !rec.Encrypted || !strings.HasSuffix(rec.Location, ".dump.age") {
		t.Errorf("managed record must be an age ciphertext: encrypted=%v location=%s", rec.Encrypted, rec.Location)
	}
	if w.wrapCalls != 1 {
		t.Errorf("Wrap called %d times, want 1", w.wrapCalls)
	}

	// What was wrapped is the identity's secret string, and that identity
	// is the one the dump was encrypted to.
	identity := unwrapIdentity(t, w, rec)
	if !bytes.Equal(w.lastSecret, []byte(identity)) {
		t.Error("Wrap was not handed the identity's secret string")
	}
	id, _ := age.ParseX25519Identity(identity)
	if rec.AgeRecipient != id.Recipient().String() {
		t.Errorf("AgeRecipient = %q, want the ephemeral identity's recipient %q", rec.AgeRecipient, id.Recipient().String())
	}
	stored, err := os.ReadFile(rec.Location)
	if err != nil {
		t.Fatal(err)
	}
	if n := recipientStanzas(stored); n != 1 {
		t.Errorf("managed-only dump has %d recipient stanzas, want 1", n)
	}
	pt, err := decryptWithIdentity(stored, identity)
	if err != nil {
		t.Fatalf("decrypt with the unwrapped identity: %v", err)
	}
	if !bytes.Equal(pt, payload) {
		t.Error("decrypted bytes differ from the dump")
	}

	// The identity never touches disk: not in the dump, not in the
	// sidecar, not anywhere under the backup dir.
	if dirContains(t, m.dir, []byte(identity)) {
		t.Fatal("the age identity string was written to disk")
	}
	if dirContains(t, m.dir, []byte("AGE-SECRET-KEY-")) {
		t.Fatal("an age identity string was written to disk")
	}

	// The sidecar carries the three new fields, named as the proto does.
	raw, err := os.ReadFile(m.sidecarPath(rec.ID))
	if err != nil {
		t.Fatal(err)
	}
	var side map[string]any
	if err := json.Unmarshal(raw, &side); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"wrapped_key", "kek_id", "key_mode"} {
		if _, ok := side[k]; !ok {
			t.Errorf("sidecar lacks %q: %s", k, raw)
		}
	}
	if side["key_mode"] != string(KeyModeManaged) {
		t.Errorf("sidecar key_mode = %v, want %q", side["key_mode"], KeyModeManaged)
	}
	// And it round-trips through Get unchanged.
	got, err := m.Get(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.WrappedKey, rec.WrappedKey) || got.KEKID != rec.KEKID || got.KeyMode != rec.KeyMode {
		t.Errorf("Get() = %+v, want the key fields of %+v", got, rec)
	}
}

func TestCreate_BothModeDecryptsWithEitherPath(t *testing.T) {
	legacy, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("PGDMP\x00plaintext-archive-bytes")
	w := newTestWrapper(t)
	m := newManagedManager(t, newFakeOps(payload), nil, w)

	rec, err := m.Create(CreateOptions{
		Username: "alice", ContainerName: "alice-container",
		Conn: PgConn{Database: "app"}, Destination: DestLocal,
		AgeRecipient: legacy.Recipient().String(),
	})
	if err != nil {
		t.Fatalf("Create (both): %v", err)
	}
	if rec.KeyMode != KeyModeBoth {
		t.Errorf("KeyMode = %q, want %q", rec.KeyMode, KeyModeBoth)
	}
	// The legacy recipient stays on the record: it is what the tenant
	// registered, and the managed path is identified by wrapped_key.
	if rec.AgeRecipient != legacy.Recipient().String() {
		t.Errorf("AgeRecipient = %q, want the legacy recipient", rec.AgeRecipient)
	}
	stored, err := os.ReadFile(rec.Location)
	if err != nil {
		t.Fatal(err)
	}
	if n := recipientStanzas(stored); n != 2 {
		t.Errorf("both-mode dump has %d recipient stanzas, want 2", n)
	}

	// Same file, two keys, same plaintext.
	viaLegacy, err := decryptWithIdentity(stored, legacy.String())
	if err != nil {
		t.Fatalf("decrypt with the legacy identity: %v", err)
	}
	viaManaged, err := decryptWithIdentity(stored, unwrapIdentity(t, w, rec))
	if err != nil {
		t.Fatalf("decrypt with the unwrapped identity: %v", err)
	}
	if !bytes.Equal(viaLegacy, payload) || !bytes.Equal(viaManaged, payload) {
		t.Error("the two decryption paths do not both yield the dump")
	}
	if dirContains(t, m.dir, []byte("AGE-SECRET-KEY-")) {
		t.Fatal("an age identity string was written to disk")
	}
}

func TestCreate_WrapFailureFailsBackup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dest   Destination
		bucket string
	}{
