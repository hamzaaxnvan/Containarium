package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/security"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #2683: InstallClamav must reach the backend that OWNS the box, the way
// TriggerClamavScan does; a box on a peer used to fail with a local lookup
// error because the install only ever ran on the daemon that was asked.

// fakeOwningPeer serves what a peer backend serves for this RPC: its container
// list (so FindContainerPeer can find the box) and the install route.
type fakeOwningPeer struct {
	srv         *httptest.Server
	installHits atomic.Int32
	gotAuth     atomic.Value // string
	gotBody     atomic.Value // string
	installCode int
	installBody string
	installWait time.Duration
}

func newFakeOwningPeer(t *testing.T, boxes ...string) *fakeOwningPeer {
	t.Helper()
	f := &fakeOwningPeer{installCode: 200, installBody: `{"outcome":"CLAMAV_INSTALL_OUTCOME_INSTALLED","message":"installed on the peer"}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/containers":
			var cs []map[string]any
			for _, b := range boxes {
				cs = append(cs, map[string]any{"name": b, "username": strings.TrimSuffix(b, "-container"), "state": "CONTAINER_STATE_RUNNING"})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"containers": cs})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/security/clamav-install":
			f.installHits.Add(1)
			f.gotAuth.Store(r.Header.Get("Authorization"))
			b, _ := io.ReadAll(r.Body)
			f.gotBody.Store(string(b))
			time.Sleep(f.installWait)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.installCode)
			_, _ = w.Write([]byte(f.installBody))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOwningPeer) client() *PeerClient {
	return &PeerClient{ID: "peer-a", Addr: f.srv.Listener.Addr().String(), Healthy: true, client: f.srv.Client()}
}

func serverWithPeers(peers ...*PeerClient) *SecurityServer {
	pp := NewPeerPool("local-vm", "", nil, "")
	for _, p := range peers {
		pp.peers[p.ID] = p
	}
	// incusClient stays nil: a request that wrongly runs locally answers
	// Unavailable, so the tests below can tell the two paths apart.
	return &SecurityServer{peerPool: pp}
}

func TestInstallClamav_ForwardsToThePeerThatOwnsTheBox(t *testing.T) {
	peer := newFakeOwningPeer(t, "alice-container")
	s := serverWithPeers(peer.client())

	resp, err := s.InstallClamav(tenantCtx("alice"), &pb.InstallClamavRequest{ContainerName: "alice-container"})

	if err != nil {
		t.Fatalf("InstallClamav: %v", err)
	}
	if resp.GetOutcome() != pb.ClamavInstallOutcome_CLAMAV_INSTALL_OUTCOME_INSTALLED || resp.GetMessage() != "installed on the peer" {
		t.Errorf("response = %v, want the peer's outcome and message", resp)
	}
	if got := peer.installHits.Load(); got != 1 {
		t.Errorf("peer install hits = %d, want exactly 1", got)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(peer.gotBody.Load().(string)), &body); err != nil || body["container_name"] != "alice-container" {
		t.Errorf("peer got body %q (%v), want {container_name: alice-container} as valid JSON", peer.gotBody.Load(), err)
	}
}

func TestInstallClamav_ForwardCarriesAJSONSafeName(t *testing.T) {
	// The existing scan forwarder formats the name into the JSON by hand; the
	// install forwarder must marshal it, so a quote in a name cannot break (or
	// inject into) the body.
	peer := newFakeOwningPeer(t, `we"ird-container`)
	s := serverWithPeers(peer.client())
	// The control plane acting for an owner it already authorized (#2692).
	ctx := auth.ContextWithTestSubjectScopes(context.Background(), "driver", []string{auth.RoleAdmin},
		[]string{auth.ScopeSecurityWrite, auth.ScopeSecurityTriggerForOwner})
	_, _ = s.InstallClamav(ctx, &pb.InstallClamavRequest{ContainerName: `we"ird-container`})
	var body map[string]string
	if err := json.Unmarshal([]byte(peer.gotBody.Load().(string)), &body); err != nil {
		t.Fatalf("forwarded body is not valid JSON: %q (%v)", peer.gotBody.Load(), err)
	}
	if body["container_name"] != `we"ird-container` {
		t.Errorf("container_name = %q, want it round-tripped intact", body["container_name"])
	}
}

func TestInstallClamav_AnOlderPeerWithoutTheRouteIsUnimplemented(t *testing.T) {
	peer := newFakeOwningPeer(t, "alice-container")
	peer.installCode, peer.installBody = 404, `{"code":5,"message":"Not Found"}`
	s := serverWithPeers(peer.client())

	_, err := s.InstallClamav(tenantCtx("alice"), &pb.InstallClamavRequest{ContainerName: "alice-container"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("got %v, want Unimplemented: an older backend is not a failed install", err)
	}
}

func TestInstallClamav_AnUnreachableBackendIsNotAnsweredLocally(t *testing.T) {
	// The only candidate peer is unhealthy: the box may live there, so the call
	// must say that, and must not fall through to the local lookup (which would
	// report the box as missing).
	s := serverWithPeers(&PeerClient{ID: "peer-down", Healthy: false})

