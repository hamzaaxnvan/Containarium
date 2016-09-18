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
		{"local", DestLocal, ""},
		{"gcs", DestGCS, "gs://bucket/pg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWrapper(t)
			w.failWrap = errBoom
			up := &fakeUploader{}
			m := newManagedManager(t, newFakeOps([]byte("archive")), up, w)

			_, err := m.Create(CreateOptions{
				Username: "alice", ContainerName: "alice-container",
				Conn: PgConn{Database: "app"}, Destination: tc.dest, GCSBucket: tc.bucket,
			})
			if err == nil {
				t.Fatal("Create succeeded despite Wrap failing")
			}
			if !strings.Contains(err.Error(), errBoom.Error()) {
				t.Errorf("error %v should carry the wrapper's reason", err)
			}
			// No file, no sidecar, no upload — and nothing listed.
			entries, _ := os.ReadDir(m.dir)
			if len(entries) != 0 {
				t.Errorf("backup dir is not empty after a wrap failure: %v", entries)
			}
			if len(up.uploadOrder) != 0 {
				t.Errorf("uploads happened after a wrap failure: %v", up.uploadOrder)
			}
			if recs, _ := m.List("alice"); len(recs) != 0 {
				t.Errorf("List = %d records, want 0", len(recs))
			}
		})
	}
}

// Without a wrapper the output is today's, byte for byte, for the parts
// that are deterministic. age randomizes a file key per encryption, so the
// ciphertext itself cannot be a golden; the sidecar is compared against a
// golden template (location/size/sha256 substituted, since they derive
// from the temp dir and the random ciphertext), and the age header is
// checked for exactly one recipient — proof that no second recipient was
// added. The plaintext path IS fully deterministic and is golden-exact.
func TestCreate_NoWrapperIsByteIdenticalToToday(t *testing.T) {
	legacy, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("PGDMP\x00plaintext-archive-bytes")

	t.Run("plaintext", func(t *testing.T) {
		m := newTestManager(t, newFakeOps(payload))
		rec, err := m.Create(CreateOptions{
			Username: "alice", ContainerName: "alice-container",
			Conn: PgConn{Database: "app"}, Destination: DestLocal,
		})
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(payload)
		want := `{
  "id": "alice-app-20260605T130405Z",
  "username": "alice",
  "database": "app",
  "created_at": "2026-06-05T13:04:05Z",
  "size_bytes": ` + strconv.Itoa(len(payload)) + `,
  "sha256": "` + hex.EncodeToString(sum[:]) + `",
  "destination": "local",
  "location": "` + filepath.Join(m.dir, "alice-app-20260605T130405Z.dump") + `",
  "engine": "postgres",
  "relation_count": 7
}`
		got, err := os.ReadFile(m.sidecarPath(rec.ID))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("sidecar differs from today's golden:\n got: %s\nwant: %s", got, want)
		}
		stored, _ := os.ReadFile(rec.Location)
		if !bytes.Equal(stored, payload) {
			t.Error("plaintext dump bytes changed")
		}
	})

	t.Run("age recipient", func(t *testing.T) {
		m := newTestManager(t, newFakeOps(payload))
		rec, err := m.Create(CreateOptions{
			Username: "alice", ContainerName: "alice-container",
			Conn: PgConn{Database: "app"}, Destination: DestLocal,
			AgeRecipient: legacy.Recipient().String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		stored, err := os.ReadFile(rec.Location)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(stored)
		want := `{
  "id": "alice-app-20260605T130405Z",
  "username": "alice",
  "database": "app",
  "created_at": "2026-06-05T13:04:05Z",
  "size_bytes": ` + strconv.Itoa(len(stored)) + `,
  "sha256": "` + hex.EncodeToString(sum[:]) + `",
  "destination": "local",
  "location": "` + filepath.Join(m.dir, "alice-app-20260605T130405Z.dump.age") + `",
  "engine": "postgres",
  "relation_count": 7,
  "encrypted": true,
  "age_recipient": "` + legacy.Recipient().String() + `"
}`
		got, err := os.ReadFile(m.sidecarPath(rec.ID))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("sidecar differs from today's golden:\n got: %s\nwant: %s", got, want)
		}
		if n := recipientStanzas(stored); n != 1 {
			t.Errorf("no-wrapper dump has %d recipient stanzas, want exactly 1", n)
		}
		if rec.KeyMode != "" || rec.WrappedKey != nil || rec.KEKID != "" {
			t.Errorf("no-wrapper record grew key fields: %+v", rec)
		}
		if pt, err := decryptWithIdentity(stored, legacy.String()); err != nil || !bytes.Equal(pt, payload) {
			t.Errorf("legacy identity no longer decrypts: %v", err)
		}
	})
}

func TestCreate_GCSUploadsSidecarAfterDump(t *testing.T) {
	w := newTestWrapper(t)
	up := &fakeUploader{}
	m := newManagedManager(t, newFakeOps([]byte("archive")), up, w)

	rec, err := m.Create(CreateOptions{
		Username: "alice", ContainerName: "alice-container",
		Conn: PgConn{Database: "app"}, Destination: DestGCS, GCSBucket: "gs://bucket/pg",
	})
	if err != nil {
		t.Fatalf("Create gcs: %v", err)
	}
	wantDump := "gs://bucket/pg/" + rec.ID + ".dump.age"
	wantSidecar := "gs://bucket/pg/" + rec.ID + ".meta.json"
	if rec.Location != wantDump {
		t.Errorf("Location = %q, want %q", rec.Location, wantDump)
	}
	if len(up.uploadOrder) != 2 || up.uploadOrder[0] != wantDump || up.uploadOrder[1] != wantSidecar {
		t.Fatalf("upload order = %v, want [dump, sidecar] = [%s %s]", up.uploadOrder, wantDump, wantSidecar)
	}
	// The bucket sidecar is the same record the host index holds — the
	// bucket alone is enough to restore after a host loss.
	var fromBucket Record
	if err := json.Unmarshal(up.blobs[wantSidecar], &fromBucket); err != nil {
		t.Fatalf("bucket sidecar is not a record: %v", err)
	}
	if fromBucket.ID != rec.ID || !bytes.Equal(fromBucket.WrappedKey, rec.WrappedKey) || fromBucket.KEKID != rec.KEKID || fromBucket.KeyMode != rec.KeyMode || fromBucket.Location != rec.Location {
		t.Errorf("bucket sidecar = %+v, want %+v", fromBucket, rec)
	}
	// The host index entry stays local too (ListBackups keeps working
	// without the bucket), and the staged dump is gone as before.
	if _, err := os.Stat(m.sidecarPath(rec.ID)); err != nil {
		t.Errorf("local sidecar missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, rec.ID+".dump.age")); !os.IsNotExist(err) {
		t.Errorf("staged dump still on the host after upload (err=%v)", err)
	}
}

// failUploadUploader fails Upload for one destURI.
type failUploadUploader struct {
	fakeUploader
	poison string
}

func (f *failUploadUploader) Upload(localPath, destURI string) error {
	if destURI == f.poison {
		return errBoom
	}
	return f.fakeUploader.Upload(localPath, destURI)
}

// A dump object without its sidecar is not a backup the bucket can
// restore from, so a sidecar upload failure fails the create and leaves
// nothing behind — same posture as a dump upload failure.
func TestCreate_GCSSidecarUploadFailureLeavesNothing(t *testing.T) {
	up := &failUploadUploader{poison: "gs://bucket/pg/alice-app-20260605T130405Z.meta.json"}
	m := NewManager(newFakeOps([]byte("archive")), up, t.TempDir())
	m.clock = fixedClock

	_, err := m.Create(CreateOptions{
		Username: "alice", ContainerName: "alice-container",
		Conn: PgConn{Database: "app"}, Destination: DestGCS, GCSBucket: "gs://bucket/pg",
	})
	if err == nil {
		t.Fatal("Create succeeded despite the sidecar upload failing")
	}
	if len(up.blobs) != 0 {
		t.Errorf("objects left in the bucket: %v", up.blobs)
	}
	if entries, _ := os.ReadDir(m.dir); len(entries) != 0 {
		t.Errorf("files left on the host: %v", entries)
	}
}

func TestPrune_GCSDeletesDumpThenSidecar(t *testing.T) {
	up := &fakeUploader{}
	m := NewManager(newFakeOps([]byte("x")), up, t.TempDir())
	m.clock = steppedClock(fixedClock(), time.Second)

	var recs []*Record
	for i := 0; i < 3; i++ {
		r, err := m.Create(CreateOptions{
			Username: "alice", ContainerName: "alice-container",
			Conn: PgConn{Database: "app"}, Destination: DestGCS, GCSBucket: "gs://bucket",
		})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		recs = append(recs, r)
	}
	up.deleteOrder = nil

	res, err := m.Prune(PruneOptions{Username: "alice", Database: "app", Keep: 1})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Failures) != 0 || len(res.Deleted) != 2 {
		t.Fatalf("Deleted=%v Failures=%v, want 2 deleted and none failed", res.Deleted, res.Failures)
	}
	// Per pruned record: dump first, then its sidecar.
	if len(up.deleteOrder) != 4 {
		t.Fatalf("delete calls = %v, want 4 (dump+sidecar per record)", up.deleteOrder)
	}
	for i := 0; i < len(up.deleteOrder); i += 2 {
		dump, side := up.deleteOrder[i], up.deleteOrder[i+1]
		if !strings.HasSuffix(dump, ".dump") || !strings.HasSuffix(side, ".meta.json") {
			t.Errorf("delete pair %d = [%s %s], want [dump, sidecar]", i/2, dump, side)
		}
		if strings.TrimSuffix(dump, ".dump") != strings.TrimSuffix(side, ".meta.json") {
			t.Errorf("delete pair %d mixes records: %s / %s", i/2, dump, side)
		}
	}
	// Both objects of each pruned record are gone; the kept one has both.
	for _, r := range recs[:2] {
		if _, ok := up.blobs[r.Location]; ok {
			t.Errorf("%s dump still in bucket", r.ID)
		}
		if _, ok := up.blobs["gs://bucket/"+r.ID+".meta.json"]; ok {
			t.Errorf("%s sidecar still in bucket", r.ID)
		}
	}
	kept := recs[2]
	if _, ok := up.blobs["gs://bucket/"+kept.ID+".meta.json"]; !ok {
		t.Errorf("kept record %s lost its bucket sidecar", kept.ID)
	}
}
