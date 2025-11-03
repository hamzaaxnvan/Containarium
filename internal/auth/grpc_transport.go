package auth

import (
	"context"
	"errors"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// The gRPC server derives a caller's identity from incoming metadata
// (SubjectFromGRPCContext). That is only safe when the metadata was written by
// a party the server trusts, so identity is bound to the *transport* the call
// arrived on and never to what the client claims:
//
//   - InternalAuthInfo: an in-process connection made through InternalListener.
//     Only the daemon's own REST gateway holds it, and it forwards claims from
//     a JWT it already verified. Metadata is trusted as-is.
//   - credentials.TLSInfo with a verified client certificate (mTLS): the
//     identity is the certificate's, not the client's. Every reserved identity
//     key the client sent is discarded.
//   - anything else (plaintext TCP, TLS without a verified client cert) is
//     refused during the handshake, and again here as defense in depth.

// InternalAuthInfo marks a connection that came through InternalListener.
type InternalAuthInfo struct{}

// AuthType implements credentials.AuthInfo.
func (InternalAuthInfo) AuthType() string { return "containarium-internal" }

// gatewayForwardMDKey mirrors audit.GatewayForwardMDKey (audit imports this
// package, so it can't be referenced here; a test in audit pins the two).
