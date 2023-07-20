package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestL4ProxyManager_EnableL4ProxyProtocol_NotActive: when L4 is not yet
// active, EnableL4ProxyProtocol must record the trusted CIDRs without error
// — ActivateL4 will produce the wrapped shape later when called by
// RouteSyncJob.
func TestL4ProxyManager_EnableL4ProxyProtocol_NotActive(t *testing.T) {
	srv := newFakeCaddy(map[string]interface{}{"apps": map[string]interface{}{}})
	defer srv.Close()
	m := NewL4ProxyManager(srv.URL)
	if err := m.EnableL4ProxyProtocol([]string{"192.0.2.13/32"}); err != nil {
		t.Fatalf("expected no-op when L4 inactive, got %v", err)
	}
	if !m.proxyProtocolEnabled() {
		t.Errorf("manager should remember trusted CIDRs even when L4 inactive")
	}
}

// TestL4ProxyManager_EnableL4ProxyProtocol_RejectsEmpty / _RejectsWildcard:
// same safety guards as the HTTP-side variant — refuse to wrap with an
// unrestricted allow list.
func TestL4ProxyManager_EnableL4ProxyProtocol_RejectsEmpty(t *testing.T) {
	m := NewL4ProxyManager("http://unreachable")
	if err := m.EnableL4ProxyProtocol(nil); err == nil {
		t.Errorf("expected error on empty CIDRs")
	}
}

func TestL4ProxyManager_EnableL4ProxyProtocol_RejectsWildcard(t *testing.T) {
	m := NewL4ProxyManager("http://unreachable")
	if err := m.EnableL4ProxyProtocol([]string{"0.0.0.0/0"}); err == nil {
		t.Errorf("expected error on 0.0.0.0/0")
	}
	if err := m.EnableL4ProxyProtocol([]string{"10.0.0.0/8", "::/0"}); err == nil {
		t.Errorf("expected error on ::/0")
	}
}

// TestL4ProxyManager_ActivateL4_WrappedWhenEnabled: when proxy-protocol is
// configured before ActivateL4, the activated server uses the pattern B
// shape directly.
func TestL4ProxyManager_ActivateL4_WrappedWhenEnabled(t *testing.T) {
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
	if err := m.SetProxyProtocolTrusted([]string{"192.0.2.13/32"}); err != nil {
		t.Fatal(err)
	}
	if err := m.ActivateL4(); err != nil {
		t.Fatalf("ActivateL4: %v", err)
	}

	cfg := readConfig(t, srv.URL)
	srvCfg := cfg["apps"].(map[string]interface{})["layer4"].(map[string]interface{})["servers"].(map[string]interface{})[L4ServerName].(map[string]interface{})
	outer := srvCfg["routes"].([]interface{})
	if len(outer) != 1 {
		t.Fatalf("expected 1 outer route (wrapped shape), got %d", len(outer))
	}
	hs := outer[0].(map[string]interface{})["handle"].([]interface{})
	if len(hs) != 2 || hs[0].(map[string]interface{})["handler"] != "proxy_protocol" {
		t.Fatalf("expected outer handlers [proxy_protocol, subroute]; got %v", hs)
	}
	innerRoutes := hs[1].(map[string]interface{})["routes"].([]interface{})
	if len(innerRoutes) != 1 {
		t.Fatalf("expected 1 inner route (catchall); got %d", len(innerRoutes))
	}
	catchallHandler := innerRoutes[0].(map[string]interface{})["handle"].([]interface{})[0].(map[string]interface{})
	if catchallHandler["proxy_protocol"] != "v2" {
		t.Errorf("catchall must emit proxy_protocol: v2; got %v", catchallHandler["proxy_protocol"])
	}
}

// TestL4ProxyManager_Lifecycle_WrappingSurvivesRouteSyncJob is the regression
// test for the prod-broke-everything bug we hit in attempt 3:
// EnableL4ProxyProtocol set up the wrap, then RouteSyncJob called
// AddL4Route / RemoveL4Route which used the old flat-routes assumption and
// clobbered the wrapping. This test simulates that exact sequence.
func TestL4ProxyManager_Lifecycle_WrappingSurvivesRouteSyncJob(t *testing.T) {
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
	mustEnable(t, m, []string{"192.0.2.13/32"})
	mustActivate(t, m)

	// Three RouteSyncJob-style sync cycles, each adding then removing routes.
	// In the old (broken) implementation, the first AddL4Route would unwrap
	// the server (because getRoutes/putRoutes went straight to outer routes,
	// not the inner subroute). After this test runs, the wrapping must still
	// be intact and the SNI routes must be inside the subroute.
	for i := 0; i < 3; i++ {
		if err := m.AddL4Route("passthrough-a.example", "203.0.113.1", 50051); err != nil {
			t.Fatalf("cycle %d AddL4Route grpc: %v", i, err)
		}
		if err := m.AddL4Route("passthrough-b.example", "203.0.113.2", 50052); err != nil {
			t.Fatalf("cycle %d AddL4Route grpc-dev: %v", i, err)
		}
		assertWrappedAndCatchallV2(t, srv.URL, i)
	}

	// Now remove one — wrapping must still hold.
	if err := m.RemoveL4Route("passthrough-b.example"); err != nil {
		t.Fatalf("RemoveL4Route: %v", err)
	}
	assertWrappedAndCatchallV2(t, srv.URL, 99)
