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
