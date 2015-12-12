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
