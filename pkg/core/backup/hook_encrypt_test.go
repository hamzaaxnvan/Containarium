package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"filippo.io/age"
)

func newLocalManager(t *testing.T, ops *fakeOps) *Manager {
	t.Helper()
	return NewManager(ops, nil, t.TempDir())
}

func execLogHas(ops *fakeOps, needle string) bool {
	for _, e := range ops.execLog {
		if strings.Contains(e, needle) {
			return true
		}
	}
	return false
}

// --- hook mode (#1831 part A: credential-less in-tenant dump) ---

func TestCreate_HookMode_RunsHookNotPgDump(t *testing.T) {
	payload := []byte("opaque-dump-bytes")
	ops := newFakeOps(payload)
	m := newLocalManager(t, ops)

	rec, err := m.Create(CreateOptions{
		Username:      "alice",
		ContainerName: "alice-container",
		Hook:          "/opt/backup/db-dump.sh",
		Destination:   DestLocal,
	})
	if err != nil {
		t.Fatalf("Create (hook): %v", err)
	}

	if rec.Engine != EngineHook {
		t.Errorf("engine = %q, want %q", rec.Engine, EngineHook)
	}
	if rec.Hook != "/opt/backup/db-dump.sh" {
		t.Errorf("record.Hook = %q", rec.Hook)
	}
	if rec.Database != "db-dump" {
		t.Errorf("database label = %q, want hook basename db-dump", rec.Database)
	}
	if rec.RelationCount != nil {
		t.Error("hook backup must not record a Postgres relation manifest")
	}
	// The hook ran (single-quoted, stdout redirected to the staging path)…
	if !execLogHas(ops, "'/opt/backup/db-dump.sh' > '/tmp/containarium-backup-") {
		t.Errorf("hook was not invoked with stdout redirected; exec log: %v", ops.execLog)
	}
	// …and pg_dump / the catalog manifest query never did — no credential path.
	if execLogHas(ops, "pg_dump") || execLogHas(ops, "pg_class") {
		t.Errorf("hook mode must not run pg_dump or the manifest query; exec log: %v", ops.execLog)
	}
	// The stored bytes are the hook's stdout, verbatim.
	got, err := os.ReadFile(rec.Location)
	if err != nil {
		t.Fatalf("read stored dump: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("stored bytes differ from hook output")
	}
}

func TestCreate_HookMode_RejectsBadHooks(t *testing.T) {
	m := newLocalManager(t, newFakeOps([]byte("x")))
	for _, bad := range []string{"relative/path.sh", "/opt/dump.sh --all", "  "} {
		_, err := m.Create(CreateOptions{Username: "alice", ContainerName: "c", Hook: bad, Destination: DestLocal})
		if err == nil {
			t.Errorf("hook %q should be refused", bad)
		}
	}
}

func TestCreate_HookMode_EmptyOutputIsAnError(t *testing.T) {
	m := newLocalManager(t, newFakeOps(nil))
	_, err := m.Create(CreateOptions{Username: "alice", ContainerName: "c", Hook: "/opt/dump.sh", Destination: DestLocal})
	if err == nil || !strings.Contains(err.Error(), "produced no output") {
		t.Errorf("expected empty-output error, got %v", err)
	}
}

func TestHookLabel(t *testing.T) {
	cases := map[string]string{
		"/opt/backup/db-dump.sh":   "db-dump",
		"/usr/local/bin/pgdump":    "pgdump",
		"/opt/x/weird name!.py":    "weird-name-",
		"/opt/.hidden":             "hook",
		"/opt/backup/a.b.c.tar.gz": "a",
	}
	for in, want := range cases {
		if got := hookLabel(in); got != want {
			t.Errorf("hookLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- encryption (#1831 part B: user-held key, ciphertext-only at rest) ---

func TestCreate_Encrypt_StoresCiphertextOnly(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("PGDMP\x00plaintext-archive-bytes")
