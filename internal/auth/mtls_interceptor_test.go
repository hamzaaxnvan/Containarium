package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Phase 2.1 — RequireMTLSUnaryInterceptor must reject calls
// without a verified mTLS peer. Audit finding C-HIGH-2.

func TestAssertMTLSPeer_NoPeerInfo(t *testing.T) {
	err := assertMTLSPeer(context.Background())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v (%v)", status.Code(err), err)
	}
}

func TestAssertMTLSPeer_NonTLSPeer(t *testing.T) {
	// peer with no AuthInfo (plaintext connection).
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: nil,
	})
	err := assertMTLSPeer(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("plaintext peer must be rejected; got %v", err)
	}
}

func TestAssertMTLSPeer_TLSWithoutClientCert(t *testing.T) {
	// TLS connection but no verified client cert — e.g. server-only
	// TLS without mTLS. Must be rejected for the mTLS path.
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				// No VerifiedChains populated.
			},
		},
	})
	err := assertMTLSPeer(ctx)
	if status.Code(err) != codes.Unauthenticated {
