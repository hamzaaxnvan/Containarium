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
const gatewayForwardMDKey = "x-containarium-gateway-forward"

// ReservedIdentityMetadataKeys are metadata keys that carry server-derived
// identity or trust markers. No untrusted party may set them.
var ReservedIdentityMetadataKeys = []string{
	MDKeyUsername, MDKeyRoles, MDKeyScopes, MDKeyAct, MDKeyJTI,
	MDKeyRunID, MDKeyTrackerConn, MDKeyRunTenant, gatewayForwardMDKey,
}

// IsReservedIdentityMetadataKey reports whether key (any case) is reserved.
func IsReservedIdentityMetadataKey(key string) bool {
	key = strings.ToLower(key)
	for _, k := range ReservedIdentityMetadataKeys {
		if key == k {
			return true
		}
	}
	return false
}

// mtlsSubjectPrefix namespaces certificate-derived subjects so a certificate
// whose CN equals a tenant username cannot pass that tenant's owner checks.
const mtlsSubjectPrefix = "mtls:"

// internalConn is the server side of a connection accepted by InternalListener.
// The type is unexported, so a connection from the network can never be one.
type internalConn struct{ net.Conn }

// InternalListener is an in-memory listener. It is not reachable from the
// network; the only way to connect is DialContext on the same instance.
type InternalListener struct{ inner *bufconn.Listener }

// NewInternalListener returns a new in-memory listener.
func NewInternalListener() *InternalListener {
	return &InternalListener{inner: bufconn.Listen(1 << 20)}
}

// Accept implements net.Listener.
func (l *InternalListener) Accept() (net.Conn, error) {
	c, err := l.inner.Accept()
	if err != nil {
		return nil, err
	}
	return &internalConn{Conn: c}, nil
}

// Close implements net.Listener.
func (l *InternalListener) Close() error { return l.inner.Close() }

// Addr implements net.Listener.
func (l *InternalListener) Addr() net.Addr { return l.inner.Addr() }

// DialContext connects to the listener; usable as grpc.WithContextDialer.
func (l *InternalListener) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	return l.inner.DialContext(ctx)
}

// serverTransportCreds accepts internal connections and, when configured,
// mTLS connections. It has no plaintext path.
type serverTransportCreds struct {
	tls credentials.TransportCredentials // nil: no external connections
}

// NewServerTransportCredentials returns server credentials that accept
// InternalListener connections, plus external connections only through tlsCreds
// (which must demand client certificates). A nil tlsCreds refuses all external
// connections.
func NewServerTransportCredentials(tlsCreds credentials.TransportCredentials) credentials.TransportCredentials {
	return &serverTransportCreds{tls: tlsCreds}
}

func (c *serverTransportCreds) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	if ic, ok := conn.(*internalConn); ok {
		return ic, InternalAuthInfo{}, nil
	}
	if c.tls == nil {
		return nil, nil, errors.New("external gRPC connections require mTLS (not enabled)")
	}
	return c.tls.ServerHandshake(conn)
