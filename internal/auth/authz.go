package auth

import (
	"context"
	"encoding/json"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Metadata keys used to propagate the authenticated subject from
// the HTTP/JWT layer through grpc-gateway into the gRPC server.
// Set by AuthMiddleware (HTTP) and by the gateway annotator
// (internal/gateway/gateway.go), read by SubjectFromGRPCContext.
//
// Phase 1.7b — `scopes` joined the trio. Stored as a comma-
// separated string in metadata (gRPC metadata is single-line
// per key, and []string is fragile across the wire).
//
// #1677 — `act` joined too. Unlike scopes it's a nested struct, not a
// flat list, so it's carried as a single JSON-encoded string rather than
// a delimited join.
// #1678 — `jti` joined too, so audit rows can name the acting credential
// ("given a token_id, what did it do"). It wasn't propagated before because
// nothing needed it off the HTTP layer; the audit package is the first
// gRPC-side consumer.
//
// #1922 — `run_id` joined too. The tracker broker's write verbs stamp
// every comment/claim with the acting run's identity (skill, model,
// run id) read from the verified token, never a request field — this
// is the hop that carries Claims.RunID from the HTTP/JWT layer to the
// gRPC handler the same way jti does.
//
// #2268 — `run_tenant` joined too: the tenant a run token acts for, which
// AuthorizeTrackerTenant matches req.Username against (the token's own
// subject is the box, not the tenant).
const (
	MDKeyUsername    = "username"
	MDKeyRoles       = "roles"
	MDKeyScopes      = "scopes"
	MDKeyAct         = "act"
	MDKeyJTI         = "jti"
	MDKeyRunID       = "run_id"
	MDKeyTrackerConn = "tracker_conn"
	MDKeyRunTenant   = "run_tenant"
)

// RoleAdmin is the role granted to operator / system tokens. Holders
// bypass per-tenant subject checks (see AuthorizeTenant). Issued by
// `containarium token generate --roles admin`.
const RoleAdmin = "admin"

// SystemSubject is the synthetic username carried by daemon-internal
// contexts (autosleep, peer-to-peer forwarders, startup tasks) that
// don't originate from a user request. Combined with the admin role
// it passes AuthorizeTenant for any target tenant.
const SystemSubject = "_system"

// ContextWithSystemIdentity returns a context tagged as the
// daemon-internal _system principal with admin role. Use it at the
// entry point of any code path that calls an RPC handler from a
// non-user context (autosleep ticker, peer forwarding, startup
// reconcilers). Without this, AuthorizeTenant rejects the call as
// Unauthenticated.
func ContextWithSystemIdentity(ctx context.Context) context.Context {
	claims := &Claims{Username: SystemSubject, Roles: []string{RoleAdmin}}
	return ContextWithClaims(ctx, claims)
}

// ContextWithTestSubject is a test-only helper that constructs a
// gRPC-incoming context with the given username and roles. Wires up
// metadata exactly the way the gateway annotator does in production.
// Lives in non-test code so multiple packages' tests can use it.
func ContextWithTestSubject(ctx context.Context, username string, roles ...string) context.Context {
	md := metadata.Pairs(MDKeyUsername, username, MDKeyRoles, strings.Join(roles, ","))
	return metadata.NewIncomingContext(ctx, md)
}

// ContextWithTestSubjectScopes is a test-only helper that also
// stamps a scopes claim. Used by Phase 1.7b RequireScope tests.
// Pass scopes=nil for an unrestricted token (matches the pre-1.7
// production path).
func ContextWithTestSubjectScopes(ctx context.Context, username string, roles []string, scopes []string) context.Context {
	pairs := []string{MDKeyUsername, username, MDKeyRoles, strings.Join(roles, ",")}
	if scopes != nil {
		pairs = append(pairs, MDKeyScopes, strings.Join(scopes, ","))
	}
	md := metadata.Pairs(pairs...)
	return metadata.NewIncomingContext(ctx, md)
}

// ContextWithTestSubjectAct is a test-only helper that also stamps a
// delegation (`act`) claim, the same way the gateway annotator does in
// production (#1677). Pass act=nil for an undelegated caller.
func ContextWithTestSubjectAct(ctx context.Context, username string, roles []string, act *Actor) context.Context {
	pairs := []string{MDKeyUsername, username, MDKeyRoles, strings.Join(roles, ",")}
	if act != nil {
		encoded, err := json.Marshal(act)
		if err == nil {
			pairs = append(pairs, MDKeyAct, string(encoded))
		}
	}
	md := metadata.Pairs(pairs...)
	return metadata.NewIncomingContext(ctx, md)
}

// ContextWithTestJTI is a test-only helper that stamps a jti onto an
// existing gRPC-incoming test context (e.g. one built by
// ContextWithTestSubject or ContextWithTestSubjectAct), the same way a
// chain of HTTP middleware would layer metadata onto a single request.
// Merges into whatever metadata is already present rather than replacing
// it, so it composes with the other ContextWithTestSubject* helpers. #1678.
