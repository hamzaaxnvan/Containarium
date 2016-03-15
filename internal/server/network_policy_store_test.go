package server

import (
	"context"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestNonNilStrings guards the fix for the egress_cidrs/egress_domains NOT NULL
// violation: a policy with no domains (or no CIDRs) arrives as a nil slice, and
// pgx would encode that as SQL NULL against a `TEXT[] NOT NULL` column. nonNilStrings
// coerces nil -> empty so the array stores '{}' instead.
func TestNonNilStrings(t *testing.T) {
	if got := nonNilStrings(nil); got == nil {
		t.Fatal("nil input must become a non-nil empty slice (else pgx writes NULL)")
	} else if len(got) != 0 {
		t.Fatalf("nil input should yield empty slice, got %v", got)
	}

	in := []string{"0.0.0.0/0", "10.0.0.0/8"}
	got := nonNilStrings(in)
	if len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Fatalf("non-nil input must pass through unchanged, got %v", got)
	}

	// Empty-but-non-nil passes through as-is (already safe).
	if got := nonNilStrings([]string{}); got == nil || len(got) != 0 {
		t.Fatalf("empty non-nil slice should stay empty non-nil, got %v", got)
	}
}

// TestMemStore_DenyRulesPersistAndSetPreserves guards the #660 persistence fix:
// deny rules survive a Set/Get round-trip (clonePolicy used to drop them), and a
// later `set` of the allow-policy must NOT wipe them (Set owns the allow-policy;
// deny rules are owned by MutateDenyRules).
