package app

import (
	"testing"
)

// TestSyncL4Routes_LatchesActiveAcrossEmptyRouteSet is the regression test for
// issue #416: the 5s route reconcile must NOT deactivate L4 (move :443 back to
// the HTTP server) when the passthrough-route set drops to zero. Deactivating
// rewrites the :443 listen address, which restarts the listener and drops
// in-flight TLS connections — surfacing as "tls: internal error" on a
// concurrent container create's response.
//
// The invariant: once L4 is active, a subsequent sync with an empty route set
// drains the SNI routes down to the catch-all but leaves L4 owning :443.
func TestSyncL4Routes_LatchesActiveAcrossEmptyRouteSet(t *testing.T) {
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
	defer srv.Close()

	m := NewL4ProxyManager(srv.URL)
	j := &RouteSyncJob{l4ProxyManager: m}

	route := &RouteRecord{
		FullDomain: "box-a.example",
		TargetIP:   "203.0.113.7",
		TargetPort: 22,
		Protocol:   string(RouteProtocolTLSPassthrough),
		Active:     true,
	}

	// 1) First passthrough route → L4 activates, :443 moves to layer4.
	if err := j.syncL4Routes([]*RouteRecord{route}); err != nil {
		t.Fatalf("syncL4Routes(1 route): %v", err)
	}
	if !m.IsL4Active() {
		t.Fatal("L4 should be active after the first passthrough route")
	}
	assertHTTPOff443(t, srv.URL)
