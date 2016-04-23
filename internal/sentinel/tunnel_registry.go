package sentinel

import (
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/safecast"
	"github.com/hashicorp/yamux"
)

// ExternalPortBase is the starting port for external tunnel proxy listeners.
// Each tunnel backend gets ExternalPortBase + index (e.g., 18001, 18002, ...).
const ExternalPortBase = 18000

// TunnelTransport names how a tunnel session reaches the sentinel.
type TunnelTransport string

const (
	// TunnelTransportTLS is a session carried over TLS with the sentinel's
	// tunnel identity.
	TunnelTransportTLS TunnelTransport = "tls"
)

// TunnelSpot represents a connected remote spot instance.
type TunnelSpot struct {
	ID           string
	Session      *yamux.Session
	LocalIP      string // assigned loopback alias, e.g. "127.0.0.2"
	ExternalPort int    // externally reachable port for API access (e.g., 18001)
	Ports        []int  // ports this spot serves
	Pool         Pool   // optional pool tag for grouping peers; empty = unpooled
	Connected    time.Time
	// Transport records how this session reached the sentinel.
	Transport TunnelTransport

	// Primary self-registration via handshake (slice 6). Non-empty
	// PublicHostname promotes this tunnel into a primary registry entry on
	// connect; cleared on disconnect.
	PublicHostname    string
	PublicAliases     []string
	PublicBaseDomains []string
	PublicPort        int

	// Generation distinguishes successive registrations of the SAME spotID.
	// A spot that reconnects (the normal case after a backend reboot or spot
	// preemption) replaces its own entry, and the goroutines watching the
	// PREVIOUS session are still alive and about to wake up. Without a way to
	// tell "my registration" from "the one that replaced me", that stale
	// watcher tears down the fresh one — see UnregisterIfCurrent (#769).
	Generation uint64
}

// The indirection seams over the `ip addr` calls. Configuring a loopback
// alias needs CAP_NET_ADMIN, which a unit test does not have and should not
// need in order to exercise registration bookkeeping — the same reason
// isAlreadyExistsErr was split out below. Production always uses the real
// functions and never writes these after init.
//
// Held in atomics rather than as plain vars because tests DO write them, and
// the tunnel server's session-monitor goroutines outlive the test that
// started them. A test restoring the seam in its cleanup then races a
// goroutine reading it — caught by -race once these tests became runnable
// without root (#1280). Production is unaffected either way; this makes the
// swap safe rather than making the read correct.
var (
	addLoopbackAliasFn    atomic.Pointer[func(string) error]
	removeLoopbackAliasFn atomic.Pointer[func(string)]
)

func init() {
	add := addLoopbackAlias
	remove := removeLoopbackAlias
	addLoopbackAliasFn.Store(&add)
	removeLoopbackAliasFn.Store(&remove)
}

// addAlias / removeAlias call whichever implementation is currently installed.
func addAlias(ip string) error { return (*addLoopbackAliasFn.Load())(ip) }
func removeAlias(ip string)    { (*removeLoopbackAliasFn.Load())(ip) }

// TunnelRegistry tracks connected tunnel clients and assigns loopback aliases.
type TunnelRegistry struct {
	mu      sync.RWMutex
	spots   map[string]*TunnelSpot
	usedIPs map[byte]string // octet -> spotID
	// nextGen hands out registration generations. Monotonic across all
	// spots; only comparisons within one spotID are meaningful.
	nextGen uint64
	// cleartextRefused counts cleartext sessions the tunnel server turned
	// away. Written from accept goroutines, read by /metrics and /status.
	cleartextRefused atomic.Uint64
}

// SessionStats returns the registered sessions split by transport plus the
// number of cleartext sessions refused so far.
func (r *TunnelRegistry) SessionStats() TunnelSessionStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var s TunnelSessionStats
	for _, spot := range r.spots {
		if spot.Transport == TunnelTransportTLS {
			s.TLS++
		}
	}
	s.CleartextRefused = r.cleartextRefused.Load()
	return s
}

// noteCleartextRefused counts one refused cleartext session.
func (r *TunnelRegistry) noteCleartextRefused() {
	r.cleartextRefused.Add(1)
}

// NewTunnelRegistry creates a new TunnelRegistry.
func NewTunnelRegistry() *TunnelRegistry {
	return &TunnelRegistry{
		spots:   make(map[string]*TunnelSpot),
		usedIPs: make(map[byte]string),
	}
}

// Register adds a new spot to the registry, assigns a loopback alias,
// and configures it on the system. Returns the assigned loopback IP.
// Pool, PublicHostname, PublicAliases, PublicPort are read off the
// handshake; PublicHostname being set means the sentinel will promote
// this tunnel into a primary registry entry on connect. transport records
// how the session reached the sentinel.
func (r *TunnelRegistry) Register(hs *TunnelHandshake, session *yamux.Session, transport TunnelTransport) (string, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	spotID := hs.SpotID

	// If this spotID is already registered, reuse its loopback IP
	// This prevents sshpiper config from going stale during reconnects
	var localIP string
	var octet byte
	if old, ok := r.spots[spotID]; ok {
		log.Printf("[tunnel-registry] spot %q reconnecting, reusing IP %s", spotID, old.LocalIP)
		if old.Session != nil {
			_ = old.Session.Close()
		}
		localIP = old.LocalIP
		for o, id := range r.usedIPs {
			if id == spotID {
				octet = o
				break
			}
		}
		delete(r.spots, spotID)

		// Re-assert the loopback alias on reconnect (#1139).
		//
		// Reusing the IP is not the same as the IP still being on `lo`. The
		// registry's map can outlive the alias — a host network
		// reconfiguration, an `ip addr flush`, or anything else that clears
		// loopback addresses leaves the entry here pointing at an address
		// the kernel no longer has. Forwarding then targets an IP that does
		// not exist, and nothing re-adds it, because this branch previously
		// only ever reused the recorded address. That is the shape operators
		// recover from by restarting the sentinel by hand.
