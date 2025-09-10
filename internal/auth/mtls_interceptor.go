package auth

import (
	"context"
	"fmt"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"google.golang.org/grpc/codes"
)

// assertMTLSPeer inspects the call's peer info and returns nil
// only when the connection carries verified mTLS credentials with
// at least one client cert. Returns Unauthenticated otherwise.
func assertMTLSPeer(ctx context.Context) error {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return status.Error(codes.Unauthenticated, "no peer info on call")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
