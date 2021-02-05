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
// accepts requests only from:
//   - loopback (127.0.0.0/8, ::1) — Caddy on the same host,
//     the production deployment shape
//   - any prefix in CONTAINARIUM_WAKE_TRUSTED_PROXIES — for
//     deployments where Caddy lives on a different VM
//
// An empty trust list with the env var unset preserves the
// pre-Phase-1.9 behavior with a startup WARNING — a backwards-
// compatible rollout step. Once operators have set the env
// var on every deployment that needs it, the warning branch
// can be removed.

const trustedProxiesEnv = "CONTAINARIUM_WAKE_TRUSTED_PROXIES"

// LoadTrustedProxies reads the env var, parses CIDR/IP entries,
// and returns the resulting prefix list. Empty list means
// "accept anything" (with a one-shot warning). Errors at parse
// time abort the load — better to log + return nil than to
// silently degrade.
func LoadTrustedProxies() ([]netip.Prefix, error) {
	raw := strings.TrimSpace(getEnv(trustedProxiesEnv))
	if raw == "" {
		log.Printf("WARNING: %s is unset — wake handler accepts requests from any source IP (audit A-MED-5)", trustedProxiesEnv)
		return nil, nil
	}
	var out []netip.Prefix
	for _, e := range strings.Split(raw, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
