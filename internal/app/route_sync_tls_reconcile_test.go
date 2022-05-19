package app

import (
	"testing"
)

// #1584 at the sync-loop level. The reported host was serving its route on
// :80 with no certificate on :443, and every 5s tick left it that way:
//
//   - syncHTTPRoutes re-provisions (AddRoute → ProvisionTLS) only when the
//     domain is MISSING from Caddy. This route was present.
//   - needsUpdate, the branch taken when the route IS present, compares
//     upstream IP, upstream port and protocol. It never looked at TLS
//     automation subjects.
//
// So "route exists, subject doesn't" was indistinguishable from "in sync".
// syncTLSSubjects closes that hole.

func httpRoute(domain string) *RouteRecord {
	return &RouteRecord{
		FullDomain: domain,
		TargetIP:   "10.0.3.7",
		TargetPort: 8080,
		Protocol:   string(RouteProtocolHTTP),
		Active:     true,
	}
}

// The exact reported state: route live in Caddy and in the DB, subject absent.
// Before the fix this tick was a no-op and the host stayed certificate-less.
func TestSyncTLSSubjects_ReconcilesRoutePresentInCaddyButMissingSubject(t *testing.T) {
	srv, fc := newRWFakeCaddy(tlsConfigWithPolicy(
		[]string{"unrelated.example.com"},
