package sshupstream

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// routingClient is a typed stub for the child router; live-binary coverage
// separately proves sshpiperd uses the advertised transport callback.
type routingClient struct {
	libplugin.SshPiperPluginClient
	callbacks      []string
	upstream       *libplugin.Upstream
	err            error
	request        *libplugin.PublicKeyAuthRequest
	verify         *libplugin.VerifyHostKeyRequest
	verifyDeadline time.Time
}

// deadlineConn records promotion from bounded SSH setup to an ongoing session.
type deadlineConn struct {
	net.Conn
	deadlines []time.Time
	closed    bool
}

// SetDeadline records the adapter's setup lifetime without sleeping in a test.
func (c *deadlineConn) SetDeadline(d time.Time) error {
	c.deadlines = append(c.deadlines, d)
	return c.Conn.SetDeadline(d)
}

// Close detects an incorrect teardown of an already established session.
func (c *deadlineConn) Close() error { c.closed = true; return c.Conn.Close() }

// TestAdapterSessionClearsSetupDeadline ensures a bounded transport hook does
// not later terminate a successfully established SSH session.
func TestAdapterSessionClearsSetupDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close(); _ = server.Close() }()
	socket := &deadlineConn{Conn: client}
	child := &routingClient{callbacks: []string{"PublicKeyAuth"}, upstream: &libplugin.Upstream{Uri: "tcp://127.0.0.1:22"}}
	cfg, err := NewAdapter(t.Context(), child, func(context.Context, Attempt) (net.Conn, error) { return socket, nil })
	if err != nil {
		t.Fatal(err)
	}
	meta := &libplugin.ConnMeta{UniqId: "bounded-setup", FromAddr: "127.0.0.2:12345"}
	u, err := cfg.PublicKeyCallback(meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.CreateConnCallback(u.Uri); err != nil {
		t.Fatal(err)
	}
	if len(socket.deadlines) != 1 || socket.deadlines[0].IsZero() {
		t.Fatal("actual setup socket has no deadline")
	}
	cfg.PipeStartCallback(&libplugin.ConnMeta{UniqId: "other-connection"})
	if len(socket.deadlines) != 1 {
		t.Fatal("another connection cleared the setup deadline")
	}
	cfg.PipeStartCallback(meta)
	if len(socket.deadlines) != 2 || !socket.deadlines[1].IsZero() || socket.closed {
		t.Fatal("accepted session retained a setup deadline or was closed")
	}
	cfg.PipeCreateErrorCallback(meta.FromAddr, errors.New("stale error"))
	if socket.closed {
		t.Fatal("stale setup failure closed an established session")
	}
}

// VerifyHostKey captures the actual backend address supplied to child verifiers.
func (c *routingClient) VerifyHostKey(ctx context.Context, req *libplugin.VerifyHostKeyRequest, _ ...grpc.CallOption) (*libplugin.VerifyHostKeyResponse, error) {
	c.verify = proto.Clone(req).(*libplugin.VerifyHostKeyRequest)
	c.verifyDeadline, _ = ctx.Deadline()
	return &libplugin.VerifyHostKeyResponse{Verified: c.err == nil}, c.err
}

// addressedConn gives an in-memory transport the backend's real address shape.
type addressedConn struct {
	net.Conn
	remote net.Addr
}

// RemoteAddr returns the independently dialed backend, never the source client.
func (c addressedConn) RemoteAddr() net.Addr { return c.remote }

// testHostKey creates synthetic public keys for pinning and mismatch checks.
func testHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// TestAdapterHostKeyPolicyIsolation exercises the existing verifier against the
// child's exact bytes and refuses missing, cross-connection and revoked pins.
func TestAdapterHostKeyPolicyIsolation(t *testing.T) {
	key, wrong := testHostKey(t), testHostKey(t)
	for _, tc := range []struct {
		name, prefix string
		wantReject   bool
	}{
		{"pinned", "", false}, {"revoked", "@revoked ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := &routingClient{callbacks: []string{"PublicKeyAuth"}, upstream: &libplugin.Upstream{Uri: "tcp://backend.example:22022", KnownHostsData: []byte(tc.prefix + knownhosts.Line([]string{"backend.example:22022"}, key) + "\n")}}
			dials := 0
			cfg, err := NewAdapter(t.Context(), child, func(context.Context, Attempt) (net.Conn, error) {
				dials++
				client, server := net.Pipe()
				t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
				return addressedConn{Conn: client, remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 22022}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			meta := &libplugin.ConnMeta{UniqId: "a", FromAddr: "127.0.0.2:12345"}
			u, err := cfg.PublicKeyCallback(meta, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.VerifyHostKeyCallback(meta, "backend.example:22022", u.Uri, key.Marshal()); err == nil {
				t.Fatal("host key accepted before a socket was dialed")
			}
			if _, err := cfg.CreateConnCallback(u.Uri); err != nil {
				t.Fatal(err)
			}
			if err := cfg.VerifyHostKeyCallback(meta, "backend.example:22022", u.Uri, key.Marshal()); (err != nil) != tc.wantReject {
				t.Fatalf("child pin policy changed: %v", err)
			}
			if err := cfg.VerifyHostKeyCallback(meta, "backend.example:22022", u.Uri, wrong.Marshal()); err == nil {
				t.Fatal("wrong backend host key accepted")
			}
			if err := cfg.VerifyHostKeyCallback(&libplugin.ConnMeta{UniqId: "other"}, "backend.example:22022", u.Uri, key.Marshal()); err == nil {
				t.Fatal("other connection borrowed the pin")
			}
			if _, err := cfg.CreateConnCallback(u.Uri); err == nil || dials != 1 {
				t.Fatal("one-use dial capability replayed")
			}
			cfg.PipeCreateErrorCallback(meta.FromAddr, errors.New("test end"))
			if err := cfg.VerifyHostKeyCallback(meta, "backend.example:22022", u.Uri, key.Marshal()); err == nil {
				t.Fatal("completed attempt retained its host-key capability")
			}
		})
	}
}

// TestAdapterForwardsChildVerifier preserves a router-owned host-key authority
// while repairing only the connection-stream's synthetic remote address.
func TestAdapterForwardsChildVerifier(t *testing.T) {
	child := &routingClient{callbacks: []string{"PublicKeyAuth", "VerifyHostKey"}, upstream: &libplugin.Upstream{Uri: "tcp://backend.example:22", KnownHostsData: []byte("child verifier overrides these bytes")}}
	client, server := net.Pipe()
	defer func() { _ = client.Close(); _ = server.Close() }()
	cfg, err := NewAdapter(t.Context(), child, func(context.Context, Attempt) (net.Conn, error) {
		return addressedConn{Conn: client, remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 22}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := &libplugin.ConnMeta{UniqId: "child-verifier", FromAddr: "127.0.0.2:12345", Metadata: map[string]string{"approval": "verified"}}
	u, err := cfg.PublicKeyCallback(meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.CreateConnCallback(u.Uri); err != nil {
		t.Fatal(err)
	}
	key := testHostKey(t).Marshal()
	if err := cfg.VerifyHostKeyCallback(meta, "backend.example:22", u.Uri, key); err != nil {
		t.Fatal(err)
	}
	if child.verify.Netaddress != "192.0.2.1:22" || !proto.Equal(child.verify.Meta, meta) || string(child.verify.Key) != string(key) {
		t.Fatalf("child verifier context changed: %v", child.verify)
