package auth

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// #1679 — scopes fail open by design (Phase 1.7 backwards compat: a token
// with no `scopes` claim is unrestricted). Strict mode is the opt-in
// backstop that flips that default for a daemon whose operator has decided
// every token must carry an explicit scope grant.
//
// Package-level state, not threaded through a config object, because
// RequireScope is a free function called from every gRPC handler across the
// tree — the same reason ScopesFromGRPCContext/etc. are package-level here.
// Set once at daemon startup (see internal/server/dual_server.go) from
// internal/config's CONTAINARIUM_STRICT_SCOPES; tests toggle it directly.
var strictScopesEnabled atomic.Bool
