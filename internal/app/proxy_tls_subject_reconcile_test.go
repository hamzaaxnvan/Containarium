package app

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #1584: TLS automation subjects were written exactly once, at route-add time
// (addRouteWithProtocol → ProvisionTLS). If that write didn't land — because
// ProvisionTLS failed and the error was swallowed (#1585), or because the
// route reached Caddy by some other path — nothing ever put the subject back.
//
// RouteSyncJob couldn't heal it either: syncHTTPRoutes only re-provisions when
// the domain is MISSING from Caddy, and needsUpdate compares upstream IP, port
// and protocol only. A route present in both the DB and Caddy but absent from
// the TLS policy was therefore classified "in sync" on every tick, forever —
// serving happily on :80 while :443 returned `tls: internal error` (alert 80,
// no peer certificate) with no error and no log line anywhere.
//
// EnsureTLSSubjects is the reconciliation these tests lock down: it is
// idempotent, batches its writes, and understands wildcard coverage so it
// doesn't burn ACME budget re-issuing what `*.<base-domain>` already covers.

// subjectsOf flattens every subject across every policy.
func subjectsOf(policies []CaddyTLSAutomationPolicy) []string {
	var out []string
	for _, p := range policies {
		out = append(out, p.Subjects...)
	}
	return out
}

func hasSubject(policies []CaddyTLSAutomationPolicy, want string) bool {
	for _, s := range subjectsOf(policies) {
		if s == want {
			return true
		}
	}
	return false
}

// THE #1584 regression: the route serves on :80, the subject was never
// written, and every prior tick considered that state correct.
func TestEnsureTLSSubjects_AddsSubjectMissingFromPolicy(t *testing.T) {
	srv, fc := newRWFakeCaddy(tlsConfigWithPolicy(
		[]string{"other.example.com"},
		[]CaddyTLSIssuer{NewACMEIssuer()},
	))
	defer srv.Close()

	p := NewProxyManager(srv.URL, "example.com")

	if err := p.EnsureTLSSubjects([]string{"app.example.com"}); err != nil {
		t.Fatalf("EnsureTLSSubjects: %v", err)
	}

	policies := readPolicies(t, fc)
	if !hasSubject(policies, "app.example.com") {
		t.Fatalf("subject was not reconciled into any policy — the host would serve on :80 "+
			"and never get a certificate (#1584); subjects: %v", subjectsOf(policies))
	}
}

// Reconciliation runs on every sync tick, so a no-op must actually be a no-op.
// Rewriting the policy array each tick would churn Caddy's config and could
// restart certificate management for every subject on the host.
func TestEnsureTLSSubjects_NoWriteWhenAllSubjectsPresent(t *testing.T) {
	srv, fc := newRWFakeCaddy(tlsConfigWithPolicy(
		[]string{"app.example.com", "api.example.com"},
		[]CaddyTLSIssuer{NewACMEIssuer()},
	))
	defer srv.Close()

	p := NewProxyManager(srv.URL, "example.com")
	before := fc.puts

	if err := p.EnsureTLSSubjects([]string{"app.example.com", "api.example.com"}); err != nil {
		t.Fatalf("EnsureTLSSubjects: %v", err)
	}

	if fc.puts != before {
		t.Fatalf("expected no config write when every subject is already present, got %d write(s)",
			fc.puts-before)
	}
}

// A `*.<base-domain>` wildcard already covers every direct child. Adding an
// explicit per-host subject on top would make Caddy issue a redundant
// single-host certificate, which is exactly the Let's Encrypt rate-limit
// exposure the wildcard path (#378) exists to avoid.
func TestEnsureTLSSubjects_WildcardCoversDirectChild(t *testing.T) {
	srv, fc := newRWFakeCaddy(tlsConfigWithPolicy(
		[]string{"*.example.com"},
		[]CaddyTLSIssuer{NewACMEIssuer()},
	))
	defer srv.Close()

	p := NewProxyManager(srv.URL, "example.com")
	before := fc.puts
