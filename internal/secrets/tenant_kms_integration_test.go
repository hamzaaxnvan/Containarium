package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration-style test against a real Postgres, same convention as
// TestSecretsStore_Roundtrip (t.Skip in CI, runnable locally with
// CONTAINARIUM_TEST_DSN set). Exercises SetTenantKMSKey /
// ClearTenantKMSKey / rewrapTenant end to end — the parts of #1630 that
// need real SQL (List/Get/Set + the tenant_kms_keys table), which the
// pure-dispatch tests in tenant_kms_test.go don't reach.
//
// The KMS side is a local fake Cloud KMS server (httptest), not real
// GCP — same reasoning kms_gcp_test.go uses. It derives a genuinely
// distinct AES key PER key-resource-name (sha256 of the path), so it
// actually enforces "the wrong key can't decrypt this ciphertext,"
// which is the property this test needs to prove.
type fakeMultiKeyGCPKMS struct{ t *testing.T }

func (f *fakeMultiKeyGCPKMS) aeadFor(keyResourceName string) cipher.AEAD {
	sum := sha256.Sum256([]byte(keyResourceName))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		f.t.Fatalf("aes.NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		f.t.Fatalf("cipher.NewGCM: %v", err)
	}
	return aead
}

func (f *fakeMultiKeyGCPKMS) handle(w http.ResponseWriter, r *http.Request) {
	// Path shape: /v1/<key-resource-name>:encrypt|:decrypt
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	op := "encrypt"
	keyResourceName := path
	if idx := strings.LastIndex(path, ":"); idx >= 0 {
		keyResourceName, op = path[:idx], path[idx+1:]
	}
	aead := f.aeadFor(keyResourceName)

	switch op {
	case "encrypt":
		var body struct {
			Plaintext string `json:"plaintext"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		pt, err := base64.StdEncoding.DecodeString(body.Plaintext)
		if err != nil {
			http.Error(w, "bad plaintext", http.StatusBadRequest)
			return
		}
		nonce := make([]byte, aead.NonceSize())
		_, _ = io.ReadFull(rand.Reader, nonce)
		ct := aead.Seal(nonce, nonce, pt, nil)
		// As Cloud KMS does, name the CryptoKeyVersion used (#2402).
		_ = json.NewEncoder(w).Encode(map[string]string{
			"name":       keyResourceName + "/cryptoKeyVersions/1",
			"ciphertext": base64.StdEncoding.EncodeToString(ct),
		})
	case "decrypt":
		var body struct {
			Ciphertext string `json:"ciphertext"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		blob, err := base64.StdEncoding.DecodeString(body.Ciphertext)
		if err != nil || len(blob) < aead.NonceSize() {
			http.Error(w, `{"error":{"message":"bad ciphertext"}}`, http.StatusBadRequest)
			return
		}
		nonce, ct := blob[:aead.NonceSize()], blob[aead.NonceSize():]
		pt, err := aead.Open(nil, nonce, ct, nil)
		if err != nil {
			// This is the load-bearing failure mode this test exists to
			// check: decrypting under the WRONG key's derived AEAD.
			http.Error(w, `{"error":{"message":"decryption failed"}}`, http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"plaintext": base64.StdEncoding.EncodeToString(pt),
		})
	default:
		http.Error(w, "unknown op", http.StatusNotFound)
	}
}

func newFakeTenantKMSFactory(t *testing.T, srv *httptest.Server) TenantKMSFactory {
	t.Helper()
	return func(keyResourceName string) (corecrypto.KMSClient, error) {
		return corecrypto.NewGCPKMS(corecrypto.GCPConfig{
			KeyName:  keyResourceName,
			Token:    "test-token",
			Endpoint: srv.URL,
		})
	}
}

func newIntegrationStore(t *testing.T, tenantFactory TenantKMSFactory) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set CONTAINARIUM_TEST_DSN to run this against Postgres (the store-integration lane does)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect Postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	key := make([]byte, corecrypto.MasterKeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := corecrypto.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	// The shared/default KMS is a fixed key on the SAME fake server —
	// exercises the "shared vs per-tenant" boundary for real, not just
	// "per-tenant vs nothing."
	fakeGCPServerURL := ""
	if tenantFactory != nil {
		// tenantFactory closes over srv.URL already; reuse it for the
		// shared client too by asking the factory to build one under a
		// fixed "shared" key name.
		shared, err := tenantFactory("projects/p/locations/l/keyRings/r/cryptoKeys/shared")
		if err != nil {
			t.Fatalf("build shared kms client: %v", err)
		}
		store, err := NewStore(ctx, pool, cipher, WithKMS(shared), WithTenantKMSFactory(tenantFactory))
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		return store, pool
	}
	_ = fakeGCPServerURL
	store, err := NewStore(ctx, pool, cipher)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, pool
}

func TestSetTenantKMSKey_RewrapsExistingSecretsUnderTheNewKey(t *testing.T) {
	fk := &fakeMultiKeyGCPKMS{t: t}
	srv := httptest.NewServer(http.HandlerFunc(fk.handle))
	defer srv.Close()

	store, pool := newIntegrationStore(t, newFakeTenantKMSFactory(t, srv))
	ctx := context.Background()
	const user = "tenant-kms-user-a"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM secrets WHERE username = $1", user)
		_, _ = pool.Exec(ctx, "DELETE FROM tenant_kms_keys WHERE username = $1", user)
	})
	_, _ = pool.Exec(ctx, "DELETE FROM secrets WHERE username = $1", user)
	_, _ = pool.Exec(ctx, "DELETE FROM tenant_kms_keys WHERE username = $1", user)

	// Write under the shared key first.
	if _, err := store.Set(ctx, user, "DATABASE_URL", "postgres://before", ""); err != nil {
		t.Fatalf("Set (shared key): %v", err)
	}

	tenantKey := "projects/p/locations/l/keyRings/r/cryptoKeys/" + user
	if err := store.SetTenantKMSKey(ctx, user, tenantKey); err != nil {
		t.Fatalf("SetTenantKMSKey: %v", err)
	}

	// The pre-existing secret must still decrypt correctly — proves the
	// rewrap actually ran, not just that the override was recorded.
	_, value, err := store.Get(ctx, user, "DATABASE_URL")
	if err != nil {
		t.Fatalf("Get after SetTenantKMSKey: %v", err)
	}
	if value != "postgres://before" {
		t.Fatalf("value after rewrap = %q, want %q", value, "postgres://before")
	}

	// A NEW secret set after the override must land under the tenant
	// key, not the shared one — check via the raw row's kek_id.
	if _, err := store.Set(ctx, user, "NEW_SECRET", "after-override", ""); err != nil {
		t.Fatalf("Set (post-override): %v", err)
	}
	var kekID string
	if err := pool.QueryRow(ctx,
		`SELECT kek_id FROM secrets WHERE username = $1 AND name = $2`, user, "NEW_SECRET",
	).Scan(&kekID); err != nil {
		t.Fatalf("query kek_id: %v", err)
	}
	if kekID != corecrypto.GCPKEKPrefix+tenantKey+"/cryptoKeyVersions/1" {
		t.Fatalf("kek_id = %q, want %q (the tenant's own key)", kekID, corecrypto.GCPKEKPrefix+tenantKey+"/cryptoKeyVersions/1")
	}
}

func TestSetTenantKMSKey_IsolatesFromOtherTenants(t *testing.T) {
	// The load-bearing security property: tenant A's key cannot
	// decrypt tenant B's secret, by construction (wrong AEAD key on
	// the fake server), not merely by authz.
	fk := &fakeMultiKeyGCPKMS{t: t}
	srv := httptest.NewServer(http.HandlerFunc(fk.handle))
	defer srv.Close()

	factory := newFakeTenantKMSFactory(t, srv)
	store, pool := newIntegrationStore(t, factory)
	ctx := context.Background()
	const userA, userB = "tenant-kms-user-a2", "tenant-kms-user-b2"
	t.Cleanup(func() {
		for _, u := range []string{userA, userB} {
			_, _ = pool.Exec(ctx, "DELETE FROM secrets WHERE username = $1", u)
			_, _ = pool.Exec(ctx, "DELETE FROM tenant_kms_keys WHERE username = $1", u)
		}
	})
	for _, u := range []string{userA, userB} {
		_, _ = pool.Exec(ctx, "DELETE FROM secrets WHERE username = $1", u)
		_, _ = pool.Exec(ctx, "DELETE FROM tenant_kms_keys WHERE username = $1", u)
	}

	keyA := "projects/p/locations/l/keyRings/r/cryptoKeys/" + userA
	keyB := "projects/p/locations/l/keyRings/r/cryptoKeys/" + userB
	if err := store.SetTenantKMSKey(ctx, userA, keyA); err != nil {
		t.Fatalf("SetTenantKMSKey A: %v", err)
	}
	if err := store.SetTenantKMSKey(ctx, userB, keyB); err != nil {
		t.Fatalf("SetTenantKMSKey B: %v", err)
	}
	if _, err := store.Set(ctx, userA, "SECRET", "a-only", ""); err != nil {
		t.Fatalf("Set A: %v", err)
	}

	// A's client (already resolved via A's override) reads A's secret fine.
	if _, value, err := store.Get(ctx, userA, "SECRET"); err != nil || value != "a-only" {
		t.Fatalf("Get A: value=%q err=%v", value, err)
	}

	// Simulate "B's key applied to A's ciphertext" directly against the
	// fake server, the way an attacker (or a bug) reaching for the
	// wrong key would: fetch A's raw envelope row, then try to Unwrap
	// its wrapped_dek using B's KMSClient instead of A's.
	var wrappedDEK []byte
	if err := pool.QueryRow(ctx,
		`SELECT wrapped_dek FROM secrets WHERE username = $1 AND name = $2`, userA, "SECRET",
	).Scan(&wrappedDEK); err != nil {
		t.Fatalf("query wrapped_dek: %v", err)
	}
	bClient, err := factory(keyB)
	if err != nil {
		t.Fatalf("build B's client: %v", err)
	}
	if _, err := bClient.Unwrap(ctx, wrappedDEK, corecrypto.GCPKEKPrefix+keyA); err == nil {
		t.Fatal("tenant B's key must NOT be able to unwrap tenant A's DEK, but it succeeded")
	}
}

// TestClearTenantKMSKey_LeavesExistingRowsOnTheirOwnKeyAndIsIdempotent pins
// the cryptographic-shred contract (Containarium-cloud's per-org-CMEK
// disclosure-gap design correction, FootprintAI/Containarium#1637):
// disabling per-org CMEK must NOT rewrap a tenant's existing secrets back
// to the shared KEK. Clear only stops routing NEW writes to the tenant's
// dedicated key — existing rows keep the kek_id they were already wrapped
// under, so once the cloud side destroys that GCP key, those rows become
// permanently unreadable. Rewrapping them to the shared key here would
// silently preserve access to pre-disable values, defeating the shred.
func TestClearTenantKMSKey_LeavesExistingRowsOnTheirOwnKeyAndIsIdempotent(t *testing.T) {
	fk := &fakeMultiKeyGCPKMS{t: t}
	srv := httptest.NewServer(http.HandlerFunc(fk.handle))
	defer srv.Close()

	store, pool := newIntegrationStore(t, newFakeTenantKMSFactory(t, srv))
	ctx := context.Background()
	const user = "tenant-kms-user-c"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM secrets WHERE username = $1", user)
		_, _ = pool.Exec(ctx, "DELETE FROM tenant_kms_keys WHERE username = $1", user)
	})
	_, _ = pool.Exec(ctx, "DELETE FROM secrets WHERE username = $1", user)
	_, _ = pool.Exec(ctx, "DELETE FROM tenant_kms_keys WHERE username = $1", user)

	tenantKey := "projects/p/locations/l/keyRings/r/cryptoKeys/" + user
	wantTenantKekID := corecrypto.GCPKEKPrefix + tenantKey + "/cryptoKeyVersions/1"
	if err := store.SetTenantKMSKey(ctx, user, tenantKey); err != nil {
		t.Fatalf("SetTenantKMSKey: %v", err)
	}
	if _, err := store.Set(ctx, user, "API_KEY", "under-tenant-key", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	kekIDOf := func(name string) string {
		t.Helper()
		var kekID string
		if err := pool.QueryRow(ctx,
			`SELECT kek_id FROM secrets WHERE username = $1 AND name = $2`, user, name,
