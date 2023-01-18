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
