package sentinel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// Per-primary daemon-AUTH health for /status (#2892).
//
// A mismatch between the sentinel's CONTAINARIUM_SENTINEL_AUTH_SECRET and a
// daemon's is otherwise visible only as 401s in logs. This records, per
// primary, whether a daemon-AUTH-gated request from its address was rejected
// with 401 since its own last authenticated registration, heartbeat or
// deregister, and when. It holds NOTHING derived from the secret: only pool
// names, times and counts.
//
// Only primaries that register over HTTP (/sentinel/primaries) are listed.
// A primary promoted by a tunnel handshake never makes those requests, so it
// is never listed, whatever its secret.
//
// Two rules keep an unauthenticated caller from creating state or poisoning
// a real primary:
//
//   - A primary's identity (its pool) is learned only from an AUTHENTICATED
//     /sentinel/primaries request, together with that request's transport
//     source address. No other request creates an entry, and the cap below
//     bounds them even then.
//   - A rejected request is attributed by its source address alone, never by
//     the pool name it carries (that name is unauthenticated). It counts
//     against a primary only when exactly one tracked primary was last seen
//     authenticating from that address; otherwise it bumps one global
//     counter, which is O(1) memory however many arrive.
//
// Residual, accepted (#3004, Q30): failing_401 can be a FALSE ALARM. Any
// request from the primary's source address that fails the signature check
// counts against that primary: no secret is needed to send one (a scanner, or
// another client on the same host or NAT, is enough), and it is not cleared by
// the passage of time. The primary's own next authenticated registration,
// heartbeat or deregister (every 30s) does clear it, but a bad-signature
// request arriving at least once per heartbeat interval keeps it set
// indefinitely. So failing_401 means "a 401 came from this address", not "this
// primary's secret is wrong": confirm in the sentinel logs that the 401s are
// the primary's own requests before rotating a secret. The reverse cannot
// happen: failures_since_success only resets on that primary's own
// authenticated registration, heartbeat or deregister (bind), not on any other
// authenticated request from the same address, so a real mismatch is never
// hidden.
//
// /status carries only a COUNT per state (never a pool name; #3004, Q29). Which
// primary is failing is served by the admin-gated primaryAuthHealthPath.

// PrimaryAuthState is the outcome of a primary's most recent daemon-AUTH
// request.
type PrimaryAuthState string

const (
	// PrimaryAuthOK: no 401 from the primary's address since its last
