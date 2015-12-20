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
func ContextWithTestJTI(ctx context.Context, jti string) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set(MDKeyJTI, jti)
	return metadata.NewIncomingContext(ctx, md)
}

// ContextWithTestRunID is a test-only helper that stamps a run_id onto an
// existing gRPC-incoming test context, the same way ContextWithTestJTI
// layers a jti on. #1922.
func ContextWithTestRunID(ctx context.Context, runID string) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set(MDKeyRunID, runID)
	return metadata.NewIncomingContext(ctx, md)
}

// ContextWithTestTrackerConn is a test-only helper that stamps a
// tracker_conn onto an existing gRPC-incoming test context, the same way
// ContextWithTestRunID layers a run_id on. #1922.
func ContextWithTestTrackerConn(ctx context.Context, conn string) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set(MDKeyTrackerConn, conn)
	return metadata.NewIncomingContext(ctx, md)
}

// ContextWithTestRunTenant is a test-only helper that stamps a run_tenant
// onto an existing gRPC-incoming test context, the same way
// ContextWithTestTrackerConn layers a tracker_conn on. #2268.
func ContextWithTestRunTenant(ctx context.Context, tenant string) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	md.Set(MDKeyRunTenant, tenant)
	return metadata.NewIncomingContext(ctx, md)
}

// SubjectFromGRPCContext returns the authenticated username and
// roles. It looks first at incoming gRPC metadata (the production
// path: HTTP middleware → gateway annotator → gRPC metadata), then
// falls back to context values for in-process gRPC calls and tests.
// The boolean is false if no subject is in either place.
func SubjectFromGRPCContext(ctx context.Context) (username string, roles []string, ok bool) {
	if md, mdOk := metadata.FromIncomingContext(ctx); mdOk {
		if vals := md.Get(MDKeyUsername); len(vals) > 0 && vals[0] != "" {
			username = vals[0]
			ok = true
		}
		if vals := md.Get(MDKeyRoles); len(vals) > 0 && vals[0] != "" {
			roles = splitRoles(vals[0])
		}
	}
	if !ok {
		if u, found := UsernameFromContext(ctx); found && u != "" {
			username = u
			ok = true
		}
	}
	if len(roles) == 0 {
		if r, found := RolesFromContext(ctx); found {
			roles = r
		}
	}
	return username, roles, ok
}

// ScopesFromGRPCContext returns the JWT's `scopes` claim
// propagated through metadata or context. Returns (nil, false)
// when the claim wasn't carried — distinct from (empty, true)
// which means an explicit empty grant. HasScope treats nil
// as "no restriction" (Phase 1.7 backwards-compat), so callers
// can pass the returned slice through to HasScope directly.
//
// That distinction is load-bearing and used to be documented here without
// being implemented (#1722): a carried-but-empty metadata value returned
// (nil, false), so "this credential holds nothing" and "this credential is
// unrestricted" produced the same answer — the permissive one. The
// context-value path below never had the bug, so the two transports
// disagreed about identical input.
//
// No production writer emits an empty value — the gateway annotator and the
// HTTP middleware both guard on len(scopes) > 0, and generate() omits the
// claim entirely for zero scopes — so correcting this changes no real
// caller's authority. What it fixes is tests: an assertion written as "a
// caller with no scopes is denied" silently passed whether or not the guard
// under test existed, because such a caller was read as unrestricted and the
// handler answered normally.
func ScopesFromGRPCContext(ctx context.Context) (scopes []string, present bool) {
	if md, mdOk := metadata.FromIncomingContext(ctx); mdOk {
		if vals := md.Get(MDKeyScopes); len(vals) > 0 {
			// Carried, so present — even when empty. ParseScopes returns nil
			// for an empty string, which HasScope would read as unrestricted,
			// so hand back a non-nil empty slice instead.
			parsed := ParseScopes(vals[0])
			if parsed == nil {
				parsed = []string{}
			}
			return parsed, true
		}
	}
	if s, found := ScopesFromContext(ctx); found {
		return s, s != nil
	}
	return nil, false
}

// ActFromGRPCContext returns the JWT's `act` delegation claim (#1677)
// propagated through metadata or context — the same
// metadata-then-context-value fallback ScopesFromGRPCContext and
// SubjectFromGRPCContext use, and for the same reason: the primary API
// surface is REST via grpc-gateway, and the caller's claims only survive
// that hop as gRPC metadata, not a context value. Returns (nil, false) when
// no delegation was carried — the valid, backward-compatible "unattributed"
// case, never an error.
