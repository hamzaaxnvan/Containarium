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
