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
