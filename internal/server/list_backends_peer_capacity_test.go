package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

// fakeSystemInfoPeer serves /v1/system/info the way a real peer daemon does
// through grpc-gateway (protojson of GetSystemInfoResponse), or a 500 when
// fail is set. It counts requests and records the bearer token it was called
// with, so tests can pin both the per-peer call budget and that the caller's
// token is what gets forwarded.
type fakeSystemInfoPeer struct {
	srv       *httptest.Server
	calls     atomic.Int32
	authSeen  atomic.Value
	unrelated atomic.Int32
}

func newFakeSystemInfoPeer(t *testing.T, info *pb.SystemInfo, fail bool) *fakeSystemInfoPeer {
	t.Helper()
	body, err := protojson.Marshal(&pb.GetSystemInfoResponse{Info: info})
	if err != nil {
		t.Fatalf("marshal peer SystemInfo: %v", err)
	}
	p := &fakeSystemInfoPeer{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/system/info" {
			p.unrelated.Add(1)
			http.NotFound(w, r)
			return
		}
		p.calls.Add(1)
		p.authSeen.Store(r.Header.Get("Authorization"))
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeSystemInfoPeer) client(id string, healthy bool) *PeerClient {
	return &PeerClient{
		ID:      id,
		Addr:    p.srv.Listener.Addr().String(),
		Healthy: healthy,
		client:  p.srv.Client(),
	}
}

// TestContainerServer_ListBackends_PeerCapacitySignals covers #2135: a
// peer's spare-capacity advertisement (#680) and capability profile (#681)
// reach its BackendInfo through the GetSystemInfo fan-out ListBackends
// already makes — one forwarded call per healthy peer, none for an
// unhealthy one — and each stays null when the peer has nothing to report
// or could not be asked.
func TestContainerServer_ListBackends_PeerCapacitySignals(t *testing.T) {
	headroom := &pb.CapacityHeadroom{
		Advertised:       true,
		SpareCpus:        6,
		SpareMemoryBytes: 16 << 30,
		SpareDiskBytes:   200 << 30,
		IdleFraction:     0.75,
		AdvertisedAt:     "2026-10-06T01:00:00Z",
	}
	profile := &pb.CapabilityProfile{
		CpuCores:      32,
		CpuModel:      "EPYC 9354",
		GpuAvailable:  true,
		GpuModel:      "L4",
		Region:        "asia-east1",
		MeasuredClass: "gpu-inference",
		ProfiledAt:    "2026-10-05T00:00:00Z",
	}

	advertising := newFakeSystemInfoPeer(t, &pb.SystemInfo{Hostname: "advertising", Headroom: headroom}, false)
	profiled := newFakeSystemInfoPeer(t, &pb.SystemInfo{Hostname: "profiled", CapabilityProfile: profile}, false)
	neither := newFakeSystemInfoPeer(t, &pb.SystemInfo{Hostname: "neither"}, false)
	failing := newFakeSystemInfoPeer(t, nil, true)
	unhealthy := newFakeSystemInfoPeer(t, &pb.SystemInfo{Hostname: "unhealthy", Headroom: headroom, CapabilityProfile: profile}, false)

	pool := NewPeerPool("local", "", nil, "")
	pool.mu.Lock()
	pool.peers["tunnel-advertising"] = advertising.client("tunnel-advertising", true)
	pool.peers["tunnel-profiled"] = profiled.client("tunnel-profiled", true)
	pool.peers["tunnel-neither"] = neither.client("tunnel-neither", true)
	pool.peers["tunnel-failing"] = failing.client("tunnel-failing", true)
