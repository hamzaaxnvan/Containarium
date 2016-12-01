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
