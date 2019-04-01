package sentinel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"
)

// TunnelClient connects from a (firewalled) spot VM outbound to the sentinel
// and serves port-forwarding requests over a yamux session.
type TunnelClient struct {
	SentinelAddr string
	// SentinelPins are the accepted SPKI pins of the sentinel's tunnel
	// identity (more than one during key rotation). Required: the client
	// connects only over TLS and only to a sentinel presenting one of them.
	SentinelPins []TunnelPin
	Token        string
	SpotID       string
	Ports        []int
	Pool         Pool // optional pool tag sent in handshake

	// When PublicHostname is set, the sentinel auto-registers this tunnel
	// as the primary for its pool. Saves the daemon from needing direct
	// HTTP access to /sentinel/primaries.
	PublicHostname    string
	PublicAliases     []string
	PublicBaseDomains []string // suffix-match anchors; see docs/PER-POOL-BASE-DOMAIN.md
	PublicPort        int

	// Forward maps an advertised port to a custom local dial target
	// (host:port). Without an entry, a stream for port N dials
	// 127.0.0.1:N. A K8s node uses this to point its advertised gateway
	// port at the in-cluster sshpiper Service's reachable address (a
	// LoadBalancer ingress or <nodeIP>:<NodePort>), since NodePorts are
	// not reliably reachable on 127.0.0.1.
	Forward map[int]string

	// sessionUpAt is when the current session was established (unix nanos), or
	// 0 when no session is up. Run uses it to tell a session that stayed up
	// from a connection attempt that never got anywhere.
	sessionUpAt atomic.Int64
}

// sessionLasted is how long the current (or just-ended) session has been up,
// or 0 if the last connection attempt never established one.
func (tc *TunnelClient) sessionLasted() time.Duration {
	up := tc.sessionUpAt.Load()
	if up == 0 {
		return 0
	}
	return time.Since(time.Unix(0, up))
}

// Run connects to the sentinel and serves tunnel traffic.
// It reconnects with exponential backoff across consecutive failures, starting
// over after a session that stayed up (see reconnectBackoff). Blocks until ctx
// is cancelled.
func (tc *TunnelClient) Run(ctx context.Context) error {
	backoff := newReconnectBackoff()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		tc.sessionUpAt.Store(0)
		err := tc.connectAndServe(ctx)
		if err != nil {
			log.Printf("[tunnel-client] connection lost: %v", err)
		}

		// Measured from when the session came up, not from when the attempt
		// began: an attempt that hung and then failed never had a session.
		delay := jitterDelay(backoff.next(tc.sessionLasted()))
		log.Printf("[tunnel-client] reconnecting to %s in %s...", tc.SentinelAddr, delay.Round(100*time.Millisecond))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (tc *TunnelClient) connectAndServe(ctx context.Context) error {
	// Connect to sentinel over TLS. The handshake and the yamux session
	// below run unchanged inside the TLS connection.
	conn, err := tc.dialTLS(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	log.Printf("[tunnel-client] connected to sentinel %s", tc.SentinelAddr)

	// Send the v2 handshake: the token stays here; the sentinel gets its
	// id and a proof bound to this TLS session.
	ekm, err := tunnelTokenEKM(conn.ConnectionState())
	if err != nil {
		return err
	}
	if err := writeHandshake(conn, tc.handshakeV2(ekm)); err != nil {
		return fmt.Errorf("write handshake: %w", err)
	}

	// Read response
	resp, err := readHandshakeResponse(conn)
	if err != nil {
		return fmt.Errorf("read handshake response: %w", err)
	}
	if !resp.OK {
