package auth

import (
	"testing"
	"time"
)

func newTestTokenManager(t *testing.T) *TokenManager {
	t.Helper()
	tm, err := NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test-issuer")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return tm
}

func TestActDepth(t *testing.T) {
	cases := []struct {
		name string
		a    *Actor
		want int
	}{
		{"nil", nil, 0},
		{"single", &Actor{Subject: "human"}, 1},
		{"nested two", &Actor{Subject: "agent-relay", Act: &Actor{Subject: "human"}}, 2},
		{
			"nested three",
			&Actor{Subject: "agent-c", Act: &Actor{Subject: "agent-b", Act: &Actor{Subject: "human"}}},
			3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := actDepth(tc.a); got != tc.want {
				t.Errorf("actDepth(%+v) = %d, want %d", tc.a, got, tc.want)
			}
		})
	}
}

func chainOfDepth(n int) *Actor {
	var a *Actor
	for i := 0; i < n; i++ {
		a = &Actor{Subject: "actor", Act: a}
	}
	return a
}

// #1678 — RootActor is how the audit package resolves the `actor` column:
// the root human/service principal at the base of a delegation chain.

func TestRootActor_NilReturnsEmpty(t *testing.T) {
	if got := RootActor(nil); got != "" {
		t.Errorf("RootActor(nil) = %q, want empty", got)
	}
}

func TestRootActor_NoDelegationReturnsOwnSubject(t *testing.T) {
	a := &Actor{Subject: "alice"}
	if got := RootActor(a); got != "alice" {
		t.Errorf("RootActor(%+v) = %q, want %q", a, got, "alice")
	}
}

func TestRootActor_WalksToBase(t *testing.T) {
	a := &Actor{Subject: "agent-c", Act: &Actor{Subject: "agent-b", Act: &Actor{Subject: "human-alice"}}}
	if got := RootActor(a); got != "human-alice" {
		t.Errorf("RootActor(%+v) = %q, want the deepest Subject %q", a, got, "human-alice")
	}
}

func TestValidateActDepth(t *testing.T) {
	if err := validateActDepth(nil); err != nil {
		t.Errorf("nil act should always pass, got %v", err)
	}
