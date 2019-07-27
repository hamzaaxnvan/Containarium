package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// Self-heal for the layer4 app after Caddy reverts to its stub config (#1067).
//
// HTTP routes already self-heal: EnsureBaseConfig runs at the top of every
// sync tick, notices the http app is gone and rebuilds it (#400). The layer4
// app had no equivalent. A Caddy crash, restart, or binary swap wipes the
// admin-API-only config, and on a host with PROXY protocol configured the
// layer4 app stayed null indefinitely — :443 went back to plain HTTP with no
// PROXY/SNI passthrough, and nothing noticed.
//
// syncL4Routes did contain an activation path, but it is guarded by
// `len(dbRoutes) == 0 → return`, so it only fires while passthrough routes
// exist. That is the gap: the observed production case had zero passthrough
// routes at the time of the wipe, so nothing ever rebuilt layer4 — and a
// route added later would have been the only thing to fix it.

// wipeCaddyConfig simulates Caddy reverting to its stub Caddyfile: the whole
// admin-API config is replaced, taking the layer4 app with it and putting the
// HTTP server back on :80,:443.
func wipeCaddyConfig(t *testing.T, adminURL string) {
	t.Helper()
	stub := map[string]interface{}{
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": map[string]interface{}{
					"srv0": map[string]interface{}{
						"listen": []interface{}{":80", ":443"},
						"routes": []interface{}{},
					},
				},
			},
		},
	}
	body, err := json.Marshal(stub)
	if err != nil {
		t.Fatalf("marshal stub: %v", err)
	}
	resp, err := http.Post(adminURL+"/load", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("simulate caddy wipe: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("simulate caddy wipe: status %d", resp.StatusCode)
	}
}

func newL4SyncJob(t *testing.T) (*RouteSyncJob, *L4ProxyManager, string) {
	t.Helper()
	initial := map[string]interface{}{
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": map[string]interface{}{
					"srv0": map[string]interface{}{
						"listen": []interface{}{":80", ":443"},
						"routes": []interface{}{},
					},
				},
			},
		},
	}
	srv := newFakeCaddy(initial)
	t.Cleanup(srv.Close)

	m := NewL4ProxyManager(srv.URL)
	return &RouteSyncJob{l4ProxyManager: m}, m, srv.URL
}

func passthroughRoute(domain string) *RouteRecord {
	return &RouteRecord{
		FullDomain: domain,
		TargetIP:   "203.0.113.7",
		TargetPort: 22,
		Protocol:   string(RouteProtocolTLSPassthrough),
		Active:     true,
	}
}

// The reported case: L4 was active, Caddy's config got wiped, and the route
// set is empty at the moment of the next sync. Without the self-heal the
// zero-route early return means layer4 is never rebuilt.
func TestSyncL4Routes_RebuildsLayer4AfterCaddyWipe_EmptyRouteSet(t *testing.T) {
	j, m, adminURL := newL4SyncJob(t)

	// L4 becomes active the normal way.
	if err := j.syncL4Routes([]*RouteRecord{passthroughRoute("box-a.example")}); err != nil {
		t.Fatalf("syncL4Routes(1 route): %v", err)
	}
	if !m.IsL4Active() {
		t.Fatal("precondition: L4 should be active after the first passthrough route")
	}

	// Caddy restarts / is swapped: config reverts to the stub.
	wipeCaddyConfig(t, adminURL)
	if m.IsL4Active() {
		t.Fatal("precondition: the wipe should have removed the layer4 app")
	}

	// The next tick, with no passthrough routes — exactly the observed case.
	if err := j.syncL4Routes(nil); err != nil {
		t.Fatalf("syncL4Routes(empty after wipe): %v", err)
	}

	if !m.IsL4Active() {
		t.Fatal("layer4 was not rebuilt after Caddy reverted to its stub config: :443 stays plain " +
			"HTTP with no PROXY/SNI passthrough, and nothing else will notice (#1067)")
	}
	assertHTTPOff443(t, adminURL)
}

// The same wipe with routes still present. This path already worked — the
// activation branch fires because dbRoutes is non-empty — and must keep
// working, including restoring the SNI routes.
func TestSyncL4Routes_RebuildsLayer4AfterCaddyWipe_WithRoutes(t *testing.T) {
