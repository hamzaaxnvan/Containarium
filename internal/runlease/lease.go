// Package runlease implements the pure lifecycle of a run's credentials.
//
// A Lease bundles a run id, the box (container) its seed files live in, and
// the one or two Credentials minted for the run. End revokes every
// credential first, then wipes the seed files out of the box, then (#1860)
// removes the seed directory and workspace directory entirely. The order is
// deliberate: revoking kills a credential the instant it is called, even if
// a copy has already left the box (exfiltrated, cached, whatever); wiping
// the two named files closes the read path inside the box itself quickly;
// removing the whole directory afterward cleans up everything else a run
// left behind (prompt, input, a fetched repo). Doing revoke after wipe would
// leave a window where a credential copied out before the wipe remains
// valid until the (later) revoke lands. Every step is always attempted, even
// when an earlier one fails, so a slow or unreachable revocation store never
// prevents the seed files from being wiped, and vice versa.
//
// End has no logging of its own — the caller logs and audits from the
// returned Outcome — which keeps this package pure and its tests
// table-driven.
package runlease

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
)

// Kind identifies what a Credential is for.
type Kind string

const (
	KindPlatformJWT  Kind = "platform_jwt"
	KindGatewayToken Kind = "gateway_token"
)

// Credential is one token minted for a run.
type Credential struct {
	Kind      Kind
	JTI       string
	ExpiresAt time.Time
}

// Lease is the run-scoped bundle of credentials and where their seed files
// live.
type Lease struct {
	RunID       string
	Box         string       // container name the seed files live in
	SeedDir     string       // e.g. /etc/containarium/agent/runs/<run_id>
	Workspace   string       // e.g. /workspace/runs/<run_id>; "" = no fetched repo for this run (#1859)
	Credentials []Credential // 1..2 today
}

// Revoker is the subset of auth.RevocationStore End needs.
