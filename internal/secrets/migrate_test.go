package secrets

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// Phase 4.1 Phase-D — pure-Go tests for the migration
// logic that don't need Postgres. The full SQL roundtrip
// is exercised by the integration suite; here we cover:
//
//   - Migrate is refused without a KMSClient
//   - The legacy → envelope round-trip on a single row
//     produces a verifiable envelope tuple
//   - The verify step catches a planted mismatch (the
//     migration would corrupt secrets if the verifier
//     were silent)

func TestMigrate_RefusedWithoutKMS(t *testing.T) {
	s := &Store{cipher: newCipher(t)}
	_, err := s.MigrateLegacyToEnvelope(context.Background(), MigrateOptions{})
	if !errors.Is(err, ErrMigrateNoKMS) {
		t.Fatalf("got %v want ErrMigrateNoKMS", err)
	}
}

// TestMigrate_SingleRowRoundtripVerify mirrors the inner
// loop of migrateOne (decrypt legacy → encrypt envelope
// → verify) without touching SQL.
func TestMigrate_SingleRowRoundtripVerify(t *testing.T) {
	cipher := newCipher(t)
	kms := newKMS(t)
	s := &Store{cipher: cipher, kms: kms}

	// Build a legacy-shaped row.
	const username, name = "alice", "FOO"
	plaintext := []byte("the-actual-secret")
	legacyNonce, legacyCT, err := cipher.Encrypt(username, name, plaintext)
	if err != nil {
		t.Fatalf("legacy encrypt: %v", err)
	}

	// Decrypt under master key (the migrator's first step).
	got, err := s.cipher.Decrypt(username, name, legacyNonce, legacyCT)
	if err != nil {
		t.Fatalf("legacy decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("legacy roundtrip broken")
	}
