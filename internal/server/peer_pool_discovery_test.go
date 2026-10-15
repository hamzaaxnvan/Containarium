package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestPeerPoolDiscoveryPoolFilter verifies that when a PeerPool is created
// with a non-empty pool tag, its sentinel discovery call appends ?pool=<name>.
// When the pool is empty, no query parameter is sent (back-compat).
func TestPeerPoolDiscoveryPoolFilter(t *testing.T) {
	tests := []struct {
		name     string
		poolArg  string
		wantPool string // expected value of ?pool= on the request, "" means absent
		wantPath string
	}{
		{name: "no pool: no query param", poolArg: "", wantPool: "", wantPath: "/sentinel/peers"},
		{name: "pool=prod-gcp", poolArg: "prod-gcp", wantPool: "prod-gcp", wantPath: "/sentinel/peers"},
		{name: "pool with special chars", poolArg: "lab/east-1", wantPool: "lab/east-1", wantPath: "/sentinel/peers"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu       sync.Mutex
				gotPath  string
				gotQuery string
				gotHas   bool
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				gotPath = r.URL.Path
				gotQuery = r.URL.Query().Get("pool")
				gotHas = r.URL.Query().Has("pool")
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"peers": []any{}})
			}))
			defer srv.Close()

			p := NewPeerPool("local", srv.URL, nil, tc.poolArg)
			p.discover()

			mu.Lock()
			defer mu.Unlock()
			if gotPath != tc.wantPath {
				t.Errorf("path: got %q, want %q", gotPath, tc.wantPath)
			}
			if tc.wantPool == "" {
				if gotHas {
					t.Errorf("expected no ?pool= query param, but got %q", gotQuery)
				}
			} else {
				if !gotHas {
					t.Errorf("expected ?pool=%q, but param was absent", tc.wantPool)
				} else if gotQuery != tc.wantPool {
					t.Errorf("?pool=: got %q, want %q", gotQuery, tc.wantPool)
				}
			}
		})
	}
}
