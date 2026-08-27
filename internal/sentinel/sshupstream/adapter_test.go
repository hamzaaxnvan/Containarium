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
