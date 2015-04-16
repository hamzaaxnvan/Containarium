package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestPeerPool_LocalPool verifies LocalPool returns the configured tag.
func TestPeerPool_LocalPool(t *testing.T) {
	cases := []struct {
		name string
		pool string
	}{
		{"empty", ""},
		{"prod", "prod"},
		{"demo", "demo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPeerPool("local", "", nil, tc.pool)
			if got := p.LocalPool(); got != tc.pool {
				t.Errorf("LocalPool() = %q, want %q", got, tc.pool)
			}
		})
	}
}

// TestPeerPool_DiscoveryParsesPool verifies that the daemon parses the
// per-peer pool tag from /sentinel/peers.
func TestPeerPool_DiscoveryParsesPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"peers": []map[string]any{
				{"id": "tunnel-demo-1", "proxy_path": "/peer/tunnel-demo-1", "pool": "demo", "healthy": true},
				{"id": "tunnel-lab-1", "proxy_path": "/peer/tunnel-lab-1", "pool": "lab", "healthy": true},
				{"id": "tunnel-untagged", "proxy_path": "/peer/tunnel-untagged", "healthy": true},
			},
		})
	}))
	defer srv.Close()

	p := NewPeerPool("local", srv.URL, nil, "")
	p.discover()

	demo := p.Get("tunnel-demo-1")
	if demo == nil || demo.Pool != "demo" {
		t.Errorf("tunnel-demo-1 pool: got %v, want demo", demo)
	}
	lab := p.Get("tunnel-lab-1")
	if lab == nil || lab.Pool != "lab" {
		t.Errorf("tunnel-lab-1 pool: got %v, want lab", lab)
	}
	un := p.Get("tunnel-untagged")
	if un == nil || un.Pool != "" {
		t.Errorf("tunnel-untagged pool: got %v, want empty", un)
	}
}

// TestPeerPool_HealthyPeersInPool verifies pool filtering with health gating.
func TestPeerPool_HealthyPeersInPool(t *testing.T) {
	p := NewPeerPool("local", "", nil, "")
	p.peers["a"] = &PeerClient{ID: "a", Pool: "demo", Healthy: true}
	p.peers["b"] = &PeerClient{ID: "b", Pool: "demo", Healthy: false}
	p.peers["c"] = &PeerClient{ID: "c", Pool: "lab", Healthy: true}
	p.peers["d"] = &PeerClient{ID: "d", Pool: "", Healthy: true}

	got := p.HealthyPeersInPool("demo")
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("HealthyPeersInPool(demo): got %+v, want [a]", got)
