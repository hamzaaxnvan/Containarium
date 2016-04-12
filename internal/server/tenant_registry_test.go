package server

import (
	"context"
	"sync"
	"testing"
)

func TestMemTenantRegistry_StableAndDistinct(t *testing.T) {
	r := NewMemTenantRegistry()
	ctx := context.Background()

	alice1, err := r.ID(ctx, "alice")
	if err != nil {
		t.Fatalf("ID(alice): %v", err)
	}
	bob, err := r.ID(ctx, "bob")
	if err != nil {
		t.Fatalf("ID(bob): %v", err)
	}
	alice2, err := r.ID(ctx, "alice")
	if err != nil {
		t.Fatalf("ID(alice) again: %v", err)
	}

	if alice1 == 0 || bob == 0 {
		t.Errorf("IDs must be non-zero (0 is reserved): alice=%d bob=%d", alice1, bob)
	}
	if alice1 != alice2 {
		t.Errorf("ID must be stable for the same tenant: %d != %d", alice1, alice2)
	}
	if alice1 == bob {
		t.Errorf("distinct tenants must get distinct IDs: alice=bob=%d", alice1)
	}
}
