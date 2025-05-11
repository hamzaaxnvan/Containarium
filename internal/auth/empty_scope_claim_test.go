package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// #1722. An empty scopes claim arriving through gRPC metadata was reported as
// ABSENT, and an absent claim reads as unrestricted — so the two opposite
// intents, "this credential holds nothing" and "this credential is
// unrestricted", produced the same answer, and it was the permissive one.
//
// The context-value path never had this problem: it reports present for any
// non-nil slice, so an empty grant denies. These pin the two paths to the same
// answer.
//
// Not reachable in production — every writer of MDKeyScopes guards on
// len(scopes) > 0, and generate() omits the claim entirely for zero scopes.
// The harm is in tests: an assertion written as "a caller with no scopes is
// denied" passes whether or not the guard under test exists, because such a
// caller is read as unrestricted and the handler answers normally.

func TestScopesFromGRPCContext_EmptyClaimIsPresentNotAbsent(t *testing.T) {
	cases := []struct {
		name        string
		md          metadata.MD
		wantPresent bool
		wantLen     int
	}{
		{
			name:        "no scopes key at all — absent, the v0 unrestricted shape",
			md:          metadata.Pairs(MDKeyUsername, "alice"),
			wantPresent: false,
		},
		{
			name:        "scopes key carried but empty — an explicit empty grant",
			md:          metadata.Pairs(MDKeyUsername, "alice", MDKeyScopes, ""),
			wantPresent: true,
			wantLen:     0,
		},
		{
			name:        "scopes carried with a value",
			md:          metadata.Pairs(MDKeyUsername, "alice", MDKeyScopes, "containers:read"),
			wantPresent: true,
			wantLen:     1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), c.md)
