//go:build !containarium_client

package cmd

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Post-join verification that the tunnel handshake actually succeeded (#1051).
//
// `pool join` used to print `Joined pool ...` and exit 0 as soon as the units
// were enabled and the host-side capability check passed. Both are host-side
// facts. Nothing asked the sentinel whether the tunnel had been accepted, so a
// tunnel being rejected on every reconnect looked exactly like a healthy join.
//
// A live BYOC host accumulated 9,423 consecutive `handshake rejected: invalid
// token` rejections over 11 days that way, with `systemctl is-active` cheerful
// throughout. The operator's only signal was a journal they had no reason to
// open.
//
// The non-obvious part, and the reason a bare "check the logs" hint is not
// enough: tunnel-join tokens DO NOT EXPIRE. Re-joining mints another token
// that is unregistered in exactly the same way, so the intuitive remedy
// reproduces the identical failure. The sentinel builds its token policy once
// at startup, so any token minted afterwards — which is every token the cloud
// BYOC flow issues — needs `sentinel register-token` before it can ever work.
// That is what the failure message has to say.

// tunnelHandshakeOutcome is what the tunnel unit's journal shows about the
// most recent handshake attempt.
type tunnelHandshakeOutcome int

