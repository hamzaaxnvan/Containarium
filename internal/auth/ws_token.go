package auth

import (
	"net/http"
	"strings"
)

// Phase 1.5 — WebSocket subprotocol auth.
//
// Browsers can't attach arbitrary headers to a `new WebSocket(url)`
// call. The two workarounds are (a) put the token in `?token=`
// (audit A-MED-3: leaks to access logs, proxy logs, browser
// history) or (b) put it in the WebSocket subprotocol header
// `Sec-WebSocket-Protocol` (the only header the browser API
// exposes on connect). We standardize on (b).
//
// Wire shape: the browser opens the socket as
//
//   new WebSocket(url, ["containarium.bearer", "<jwt>"])
//
// which sets `Sec-WebSocket-Protocol: containarium.bearer, <jwt>`.
// The server reads the second token, validates the JWT, and
// **acks the subprotocol back as just "containarium.bearer"**
// (the JWT itself is not echoed — RFC 6455 requires the ack to
// match one of the client's offered subprotocols, but gorilla
// lets us pick which one).
//
// Backwards compatibility: Authorization header and `?token=`
// query are still accepted, but `?token=` use is logged with
// a deprecation WARNING so operators can find affected clients.
// Once telemetry shows no `?token=` traffic, the query path will
// be removed.

const (
	// WSSubprotocolBearer is the WebSocket subprotocol name
	// clients announce when they want to attach a JWT via the
	// Sec-WebSocket-Protocol header. The server echoes this
	// value back (without the token) to complete the upgrade.
	WSSubprotocolBearer = "containarium.bearer"
)

// TokenSource describes where a bearer token was extracted
// from, for logging / deprecation telemetry.
type TokenSource int

const (
	TokenSourceNone TokenSource = iota
	TokenSourceSubprotocol
	TokenSourceAuthorizationHeader
	TokenSourceQueryParam
)

func (s TokenSource) String() string {
	switch s {
	case TokenSourceSubprotocol:
		return "subprotocol"
	case TokenSourceAuthorizationHeader:
		return "authorization-header"
	case TokenSourceQueryParam:
		return "query-param"
	default:
		return "none"
	}
}

// ExtractBearerForUpgrade pulls a bearer token from an HTTP
// request that is about to be upgraded to WebSocket (or a
// streaming endpoint like SSE). Search order:
//
//  1. Sec-WebSocket-Protocol — first non-"containarium.bearer"
//     entry. This is the preferred form.
//  2. Authorization: Bearer <token>
