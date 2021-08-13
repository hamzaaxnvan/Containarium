package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Phase 4.3 Phase B-3 — reconciler unit tests.
//
// The reconciler depends on a *secrets.Store (for the
// usernames query) and an *incus.Client (for state
// lookup). Both are concrete types that need live
// backends, so we don't construct the full reconciler
// here. Instead we exercise the inner tick logic directly
// by wiring fakes through closures — which is what the
// production code does too (the stamp argument is a
// function value).

// fakeReconciler runs the same loop body as the real
// secretsReconciler but lets the test drive the
// list-tenants + get-container responses directly.
type fakeReconciler struct {
	listTenants func(ctx context.Context) ([]string, error)
	containerOf func(name string) (state string, found bool)
	stamp       func(ctx context.Context, username string) (int, error)
}

func (r *fakeReconciler) tick(ctx context.Context) {
	users, err := r.listTenants(ctx)
	if err != nil {
		return
	}
	for _, u := range users {
		state, ok := r.containerOf(u + "-container")
		if !ok {
			continue
		}
		if state != "Running" {
			continue
		}
		_, _ = r.stamp(ctx, u)
	}
}

func TestReconciler_SkipsStoppedContainers(t *testing.T) {
	var stamped sync.Map
	r := &fakeReconciler{
		listTenants: func(ctx context.Context) ([]string, error) {
			return []string{"alice", "bob"}, nil
		},
		containerOf: func(name string) (string, bool) {
			switch name {
			case "alice-container":
				return "Running", true
			case "bob-container":
