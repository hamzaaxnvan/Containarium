package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// shrinkProbeTimeout makes the per-backend probe budget small for a test.
func shrinkProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := listBackendsProbeTimeout
	listBackendsProbeTimeout = d
	t.Cleanup(func() { listBackendsProbeTimeout = old })
}

func addTestPeer(pool *PeerPool, id string, srv *httptest.Server, healthy bool) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.peers[id] = &PeerClient{ID: id, Addr: srv.Listener.Addr().String(), Healthy: healthy, client: srv.Client()}
}

// Peers that never answer must be probed concurrently and bounded by the
// per-call budget: the listing returns promptly, the hung peers keep identity
// and health but carry no load, and a responsive peer still reports.
func TestListBackends_UnresponsivePeersDoNotBlockListing(t *testing.T) {
	shrinkProbeTimeout(t, 300*time.Millisecond)

	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); hang.Close() })
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"info":{"hostname":"peer-ok","os":"linux"}}`))
	}))
	t.Cleanup(ok.Close)

	pool := NewPeerPool("local-spot", "", nil, "")
	addTestPeer(pool, "peer-hang-1", hang, true)
	addTestPeer(pool, "peer-hang-2", hang, true)
	addTestPeer(pool, "peer-hang-3", hang, true)
	addTestPeer(pool, "peer-ok", ok, true)

	s := &ContainerServer{peerPool: pool}
	ctx := auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)

	start := time.Now()
	resp, err := s.ListBackends(ctx, &pb.ListBackendsRequest{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ListBackends: %v", err)
	}
	// Serial probing of 3 hung peers would take >= 900ms.
	if elapsed > 700*time.Millisecond {
		t.Fatalf("listing took %v; hung peers must be probed in parallel under the per-call budget", elapsed)
	}
	byID := map[string]*pb.BackendInfo{}
	for _, b := range resp.Backends {
		byID[b.Id] = b
	}
	if len(resp.Backends) != 5 {
		t.Fatalf("got %d backends, want local + 4 peers", len(resp.Backends))
	}
	for _, id := range []string{"peer-hang-1", "peer-hang-2", "peer-hang-3"} {
		b := byID[id]
		if b == nil || !b.Healthy || b.HostLoad != nil || b.Hostname != "" {
			t.Errorf("%s: want identity+health only, got %+v", id, b)
		}
	}
	if byID["peer-ok"].Hostname != "peer-ok" {
