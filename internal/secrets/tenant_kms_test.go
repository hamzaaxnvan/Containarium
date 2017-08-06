package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/footprintai/containarium/internal/safecast"
	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
)

// #1630 — per-tenant KEK dispatch. These tests avoid Postgres entirely
// (same discipline as envelope_dispatch_test.go): they construct a
// *Store literal directly and exercise resolveEncryptKMS /
// resolveDecryptKMS / kekClient, which are pure in-memory + factory
// logic. SetTenantKMSKey / ClearTenantKMSKey / rewrapTenant need a real
// Postgres (they call List/Get/Set) — those live in
// tenant_kms_integration_test.go, gated on CONTAINARIUM_TEST_DSN like
// the rest of the Postgres-backed suite.

// fakeTenantKMS is a KMSClient double whose kek_id is settable per
// instance, so tests can prove "this exact client" was chosen rather
// than merely "a client was chosen." Wrap/Unwrap are otherwise
// pass-through (XOR with a per-instance byte) — enough to prove a
// value only round-trips through the SAME instance, catching a
// resolver that returns the wrong client for decrypt.
type fakeTenantKMS struct {
	kekID   string
	tweak   byte
	wrapErr error
}

func (f *fakeTenantKMS) Wrap(_ context.Context, dek []byte) ([]byte, string, error) {
	if f.wrapErr != nil {
		return nil, "", f.wrapErr
	}
	out := make([]byte, len(dek))
	for i, b := range dek {
		out[i] = b ^ f.tweak
	}
	return out, f.kekID, nil
}

func (f *fakeTenantKMS) Unwrap(_ context.Context, wrapped []byte, kekID string) ([]byte, error) {
	if kekID != f.kekID {
		return nil, errors.New("fakeTenantKMS: kek_id mismatch — wrong client selected")
	}
	out := make([]byte, len(wrapped))
	for i, b := range wrapped {
		out[i] = b ^ f.tweak
	}
	return out, nil
}

func newFakeFactory(t *testing.T, built map[string]int) TenantKMSFactory {
	t.Helper()
	return func(keyResourceName string) (corecrypto.KMSClient, error) {
		if keyResourceName == "bad-key" {
			return nil, errors.New("fake: malformed key")
		}
		built[keyResourceName]++
		// Deterministic per-key tweak byte so different keys are
		// distinguishable but the same key always builds an
		// equivalent (thought not necessarily identical, since the
		// cache should prevent rebuilding) client.
		return &fakeTenantKMS{kekID: corecrypto.GCPKEKPrefix + keyResourceName, tweak: safecast.U8(len(keyResourceName))}, nil
	}
}

func TestResolveEncryptKMS_NoFactory_UsesSharedDefault(t *testing.T) {
	shared := &fakeTenantKMS{kekID: "shared"}
	s := &Store{kms: shared}
	got, err := s.resolveEncryptKMS("alice")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != shared {
		t.Fatal("expected the shared client when no tenant factory is configured")
	}
}

func TestResolveEncryptKMS_NoOverride_UsesSharedDefault(t *testing.T) {
	shared := &fakeTenantKMS{kekID: "shared"}
	built := map[string]int{}
	s := &Store{
		kms:              shared,
		tenantKMSFactory: newFakeFactory(t, built),
		tenantKEK:        map[string]string{},
		kekClients:       map[string]corecrypto.KMSClient{},
	}
	got, err := s.resolveEncryptKMS("alice")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != shared {
		t.Fatal("alice has no override — must resolve to the shared client")
	}
	if len(built) != 0 {
		t.Fatal("must not build a tenant client when there's no override to serve")
	}
}

func TestResolveEncryptKMS_WithOverride_BuildsAndCachesTenantClient(t *testing.T) {
	shared := &fakeTenantKMS{kekID: "shared"}
	built := map[string]int{}
	s := &Store{
		kms:              shared,
		tenantKMSFactory: newFakeFactory(t, built),
		tenantKEK:        map[string]string{"alice": "tenant-key-a"},
		kekClients:       map[string]corecrypto.KMSClient{},
	}
	got, err := s.resolveEncryptKMS("alice")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got == shared {
		t.Fatal("alice has an override — must NOT resolve to the shared client")
	}
	if built["tenant-key-a"] != 1 {
