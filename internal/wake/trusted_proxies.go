package wake

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Phase 1.9 — trusted-proxy gate on the wake handler
// (audit A-MED-5).
//
// /wake/ and the root catch-all are intentionally outside the
// JWT auth middleware — Caddy forwards arbitrary user traffic
// to the wake handler when a container is in scale-to-zero
// state, and that traffic doesn't carry a daemon JWT.
//
// But "outside auth" means an attacker who can reach the
// daemon port directly (bypassing Caddy via a misrouted
// firewall, a leaked internal IP, an SSRF chain) can wake any
// container by crafting the Host header. The wake itself isn't
// directly destructive, but it triggers container starts that
// consume real CPU/disk/network — fine as a side effect of
// genuine user traffic, dangerous as a primitive an attacker
// can repeatedly invoke.
//
// The fix is a source-IP allowlist. The wake handler now
