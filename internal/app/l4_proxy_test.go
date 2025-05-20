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
