package auth

import "testing"

func TestSetStrictScopes_TogglesState(t *testing.T) {
	defer SetStrictScopes(false)

	SetStrictScopes(true)
	if !StrictScopesEnabled() {
		t.Fatal("StrictScopesEnabled() = false after SetStrictScopes(true)")
	}
	SetStrictScopes(false)
	if StrictScopesEnabled() {
		t.Fatal("StrictScopesEnabled() = true after SetStrictScopes(false)")
	}
}

func TestStrictScopesEnabled_DefaultsFalse(t *testing.T) {
	// Package-level default before anything calls SetStrictScopes — matches
	// the AC "default remains permissive."
	if StrictScopesEnabled() {
		t.Fatal("StrictScopesEnabled() = true with no SetStrictScopes call; default must be permissive")
	}
}

func TestUnscopedTokenCalls_TracksCountAndSince(t *testing.T) {
	ResetUnscopedTokenCallsForTest()

	if got := UnscopedTokenCalls(); got.Count != 0 || !got.Since.IsZero() {
