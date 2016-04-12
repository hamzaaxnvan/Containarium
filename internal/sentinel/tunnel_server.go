package sentinel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// tunnelTLSRequiredError is the answer a cleartext peer gets. It is written
// as the ordinary handshake response so an older client prints a readable
// reason instead of hanging.
const tunnelTLSRequiredError = "tls required; upgrade the tunnel client and set --sentinel-pin"

// DefaultTunnelAPIBindAddr is the IP the per-backend API listener
// (ExternalPortBase+N, forwarding to the backend's port 8080) binds to by
// default. Everything on the sentinel reaches a backend through its loopback
// alias or the tunnel registry; set TunnelServer.APIBindAddr to an internal
// address only when a daemon on another host lists this port in --peers.
const DefaultTunnelAPIBindAddr = "127.0.0.1"

// TunnelServer listens for incoming tunnel connections from remote spot instances.
// Each connected spot gets a loopback alias, and the server opens local TCP proxy
// listeners that forward traffic through the yamux session to the spot.
//
// A session arrives over TLS: a ClientHello offering TunnelALPN, terminated
// here with the identity set by SetTunnelIdentity. A peer that opens in the
// clear is answered with tunnelTLSRequiredError and closed.
type TunnelServer struct {
	listenAddr string
	policy     *TokenPolicy
	registry   *TunnelRegistry

	// httpsPort is the sentinel's own ConnMux HTTPS port (the maintenance/SNI
	// listener's wildcard bind). No spot ever gets a loopback listener for
	// it — see loopbackPortsFor. Zero disables the exclusion (e.g. in tests
	// that don't run a ConnMux).
	httpsPort int

	// APIBindAddr is the IP the per-backend API listener binds to.
	// NewTunnelServer sets DefaultTunnelAPIBindAddr; empty means the same.
	APIBindAddr string

	// tlsConfig terminates TLS tunnel sessions. Nil until SetTunnelIdentity
	// is called; TLS peers are closed while it is nil.
	tlsConfig *tls.Config

	// Callbacks for Manager integration
	OnConnect    func(spot *TunnelSpot)
	OnDisconnect func(spot *TunnelSpot)

	// Active proxy listeners per spot. Keyed by spotID and tagged with the
	// registration generation that installed them, so a superseded watcher
	// cannot close the listeners belonging to the registration that replaced
	// it (#769).
	mu        sync.Mutex
	proxies   map[string]proxySet
	cancelCtx context.CancelFunc

	// goroutines counts every goroutine the server starts on behalf of a
	// tunnel session (connection handlers, session monitors, proxy loops and
	// proxied connections), so Wait can report when none is left.
	goroutines sync.WaitGroup
}

// proxySet is one registration's listeners plus the generation that owns them.
type proxySet struct {
	gen       uint64
	listeners []net.Listener
}

// NewTunnelServer creates a new tunnel server with a token-bound pool
// authorization policy. To preserve the legacy single-token behavior
// (any pool allowed), pass a policy with a single rule:
//
//	policy := NewTokenPolicy()
//	policy.Allow(token, "*")
//	srv := NewTunnelServer(addr, policy, registry, httpsPort)
//
// httpsPort is the sentinel's own ConnMux HTTPS port; pass 0 if this
// TunnelServer never runs alongside a ConnMux (e.g. in a test).
func NewTunnelServer(listenAddr string, policy *TokenPolicy, registry *TunnelRegistry, httpsPort int) *TunnelServer {
	return &TunnelServer{
		listenAddr:  listenAddr,
		policy:      policy,
		registry:    registry,
		httpsPort:   httpsPort,
		APIBindAddr: DefaultTunnelAPIBindAddr,
		proxies:     make(map[string]proxySet),
	}
}

// SetTunnelIdentity installs the identity presented to TLS tunnel peers.
// Call it before Serve.
func (ts *TunnelServer) SetTunnelIdentity(id *TunnelIdentity) {
	ts.tlsConfig = tunnelTLSServerConfig(id)
}

// tunnelTLSServerConfig is the listener side of the tunnel transport: the
// identity's certificate, TLS 1.3 only, the tunnel ALPN protocol, and no
// client certificate (the peer is authenticated by the token inside). It
// is separate from the HTTPS configs that serve the certificate store.
func tunnelTLSServerConfig(id *TunnelIdentity) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.TLSCertificate()},
		NextProtos:   []string{TunnelALPN},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.NoClientCert,
	}
}

// acceptTransport classifies a freshly accepted connection by its first
// byte and returns the connection the handshake is read from. A TLS record
// (0x16) is terminated with the tunnel identity and must negotiate
// TunnelALPN. Anything else is answered with tunnelTLSRequiredError before
// its handshake line is read and counted as refused. The caller closes the
// returned connection on error.
func (ts *TunnelServer) acceptTransport(conn net.Conn) (net.Conn, TunnelTransport, error) {
	first := make([]byte, 1)
	if _, err := io.ReadFull(conn, first); err != nil {
		return conn, "", fmt.Errorf("read first byte: %w", err)
	}
	conn = &peekedConn{Conn: conn, peeked: first}

	if first[0] != 0x16 {
		ts.registry.noteCleartextRefused()
		_ = writeHandshakeResponse(conn, &TunnelHandshakeResponse{OK: false, Error: tunnelTLSRequiredError})
		return conn, "", errors.New("cleartext tunnel session refused: tls required")
	}

	if ts.tlsConfig == nil {
		return conn, "", errors.New("TLS tunnel session but no tunnel identity is configured")
	}
	tlsConn := tls.Server(conn, ts.tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		return tlsConn, "", fmt.Errorf("tunnel TLS handshake: %w", err)
	}
	if proto := tlsConn.ConnectionState().NegotiatedProtocol; proto != TunnelALPN {
		return tlsConn, "", fmt.Errorf("tunnel TLS session negotiated ALPN %q, want %q", proto, TunnelALPN)
	}
	return tlsConn, TunnelTransportTLS, nil
}

// spawn runs f on a goroutine tracked by Wait.
func (ts *TunnelServer) spawn(f func()) {
	ts.goroutines.Add(1)
	go func() {
		defer ts.goroutines.Done()
		f()
	}()
}

// Wait blocks until every goroutine the server started for a tunnel session
// has exited. It does not stop anything: cancel the context passed to Serve
// and close or unregister the sessions first, or it waits for them forever.
// Tests use it on cleanup so no session goroutine (notably monitorSession,
// whose cleanup logs and runs `ip addr del`) outlives the test that owns it.
func (ts *TunnelServer) Wait() { ts.goroutines.Wait() }

// Run starts the tunnel server on its own port. Blocks until ctx is cancelled.
func (ts *TunnelServer) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", ts.listenAddr)
	if err != nil {
		return fmt.Errorf("tunnel server listen on %s: %w", ts.listenAddr, err)
	}
	defer func() { _ = ln.Close() }()

	log.Printf("[tunnel-server] listening on %s", ts.listenAddr)
	return ts.Serve(ctx, ln)
}

// Serve accepts tunnel connections from the given listener. Use this when the
// listener is provided externally (e.g., from a ConnMux). Blocks until ctx is
// cancelled or the listener is closed.
func (ts *TunnelServer) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	ts.cancelCtx = cancel

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				// Check if the listener was closed (net.ErrClosed or similar)
				if isClosedErr(err) {
					return nil
				}
				log.Printf("[tunnel-server] accept error: %v", err)
				continue
			}
		}
		ts.spawn(func() { ts.handleConnection(ctx, conn) })
	}
}

func (ts *TunnelServer) handleConnection(ctx context.Context, conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[tunnel-server] new connection from %s", remoteAddr)

	// Set deadline for the transport setup and the handshake
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	conn, transport, err := ts.acceptTransport(conn)
	if err != nil {
		log.Printf("[tunnel-server] %s: %v", remoteAddr, err)
		_ = conn.Close()
		return
	}

	// Read handshake
	hs, err := readHandshake(conn)
	if err != nil {
		log.Printf("[tunnel-server] handshake read error from %s: %v", remoteAddr, err)
		_ = conn.Close()
		return
	}

	// Validate. The handshake must be v2 and its proof must match the
	// keying material this end exports from the same session.
	if err := ts.validate(conn, hs); err != nil {
		log.Printf("[tunnel-server] handshake validation failed from %s: %v", remoteAddr, err)
		_ = writeHandshakeResponse(conn, &TunnelHandshakeResponse{OK: false, Error: err.Error()})
		_ = conn.Close()
		return
	}

	// Clear deadline after successful handshake validation
	_ = conn.SetDeadline(time.Time{})

	// Create yamux session. The sentinel is the yamux *client* (it opens streams
	// to dial into the spot), even though the TCP connection was initiated by the spot.
	yamuxCfg := yamux.DefaultConfig()
	yamuxCfg.EnableKeepAlive = true
	// Generous timeouts so tunnel survives CPU-heavy workloads on peers
	yamuxCfg.KeepAliveInterval = 60 * time.Second
	yamuxCfg.ConnectionWriteTimeout = 60 * time.Second

	session, err := yamux.Client(conn, yamuxCfg)
	if err != nil {
		log.Printf("[tunnel-server] yamux client creation failed for %s: %v", hs.SpotID, err)
		_ = writeHandshakeResponse(conn, &TunnelHandshakeResponse{OK: false, Error: "yamux init failed"})
		_ = conn.Close()
		return
	}

	// Register in the registry (assigns loopback alias)
	localIP, gen, err := ts.registry.Register(hs, session, transport)
	if err != nil {
		log.Printf("[tunnel-server] registration failed for %s: %v", hs.SpotID, err)
		_ = writeHandshakeResponse(conn, &TunnelHandshakeResponse{OK: false, Error: err.Error()})
		_ = session.Close()
		return
	}

	// Send success response
	if err := writeHandshakeResponse(conn, &TunnelHandshakeResponse{OK: true, AssignedIP: localIP}); err != nil {
		log.Printf("[tunnel-server] failed to send handshake response to %s: %v", hs.SpotID, err)
		ts.registry.Unregister(hs.SpotID)
		_ = session.Close()
		return
	}

	log.Printf("[tunnel-server] spot %q authenticated over %s, assigned %s, ports %v", hs.SpotID, transport, localIP, hs.Ports)

	// Start local TCP proxy listeners for each port. Skip the sentinel's own
	// HTTPS ConnMux port for every backend, not just tunnel-promoted
	// primaries — the ConnMux always holds a wildcard bind on it, so a
	// per-spot loopback listener there can never succeed, and the SNI router
	// already reaches every backend's HTTPS traffic via
	// TunnelRegistry.DialTunnel() instead (#1710). Also skip a promoted
	// primary's PublicPort in case it differs from the ConnMux's port.
	externalPort := 0
	if spot := ts.registry.Get(hs.SpotID); spot != nil {
		externalPort = spot.ExternalPort
	}
	loopbackPorts := loopbackPortsFor(hs.Ports, ts.httpsPort, hs.PublicPort)
	ts.startProxies(ctx, hs.SpotID, gen, localIP, externalPort, loopbackPorts, session)

	// Notify manager
	spot := ts.registry.Get(hs.SpotID)
	if spot != nil && ts.OnConnect != nil {
		ts.OnConnect(spot)
	}

	// Monitor session — when it closes, clean up. The generation is what
	// keeps this cleanup from firing against a LATER registration of the same
	// spot (#769).
	ts.spawn(func() { ts.monitorSession(hs.SpotID, gen, session) })
}

// validate checks a handshake read from a TLS tunnel session against the
// keying material exported from that session.
func (ts *TunnelServer) validate(conn net.Conn, hs *TunnelHandshake) error {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return errors.New("tls tunnel session without tls state")
	}
	ekm, err := tunnelTokenEKM(tlsConn.ConnectionState())
	if err != nil {
		return err
	}
	return validateHandshakeV2(hs, ts.policy, ekm)
}

