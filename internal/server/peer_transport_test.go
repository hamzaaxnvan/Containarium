package server

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func capturePeerLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestPeerTransport_Value(t *testing.T) {
	standalone := NewPeerPool("b1", "", nil, "")
	withSentinel := NewPeerPool("b1", "https://sentinel.example.com:8889", nil, "")
	withPeers := NewPeerPool("b1", "", []string{"peer.example.com:8080"}, "")
	secured := NewPeerPool("b1", "https://sentinel.example.com:8889", nil, "")
	secured.pki = &peerPKI{}
	lateStatic := NewPeerPool("b1", "", nil, "")
	lateStatic.peers["p"] = &PeerClient{ID: "p"} // as discovery would add

	tests := []struct {
		name string
		pool *PeerPool
		want pb.PeerTransport
	}{
		{"nil pool", nil, pb.PeerTransport_PEER_TRANSPORT_UNSPECIFIED},
		{"standalone: no sentinel, no peers", standalone, pb.PeerTransport_PEER_TRANSPORT_UNSPECIFIED},
		{"sentinel URL, no pki", withSentinel, pb.PeerTransport_PEER_TRANSPORT_HTTP},
		{"static peers, no pki", withPeers, pb.PeerTransport_PEER_TRANSPORT_HTTP},
		{"peer added after construction", lateStatic, pb.PeerTransport_PEER_TRANSPORT_HTTP},
		{"peer pki bootstrapped", secured, pb.PeerTransport_PEER_TRANSPORT_MTLS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := peerTransportOf(tt.pool); got != tt.want {
				t.Fatalf("peerTransportOf = %v, want %v", got, tt.want)
			}
		})
	}
	if !secured.PeerMTLSEnabled() {
		t.Fatal("secured pool: PeerMTLSEnabled = false, want true")
	}
	if withSentinel.PeerMTLSEnabled() {
		t.Fatal("pool without pki: PeerMTLSEnabled = true, want false")
	}
	if enabled, relevant := standalone.PeerMTLSState(); enabled || relevant {
		t.Fatalf("standalone state = (%v,%v), want (false,false)", enabled, relevant)
	}
}

func TestPeerPool_LogPlainHTTPOnce_StandaloneSilent(t *testing.T) {
	buf := capturePeerLog(t)
	p := NewPeerPool("b1", "", nil, "")
	p.LogPlainHTTPOnce(nil)
	if buf.Len() != 0 {
		t.Fatalf("standalone daemon must log nothing, got %q", buf.String())
	}
}
