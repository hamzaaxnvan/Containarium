package sentinel

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultReachabilityInterval is how often checkTunnelPrimaries sweeps
// every tunnel-promoted primary when Config.PrimaryReachabilityInterval
// is unset.
const defaultReachabilityInterval = 30 * time.Second

// defaultReachabilityTimeout bounds one probeHostname call (dial + TLS
// handshake + one HTTP round trip) so a wedged spot can't stall the sweep.
const defaultReachabilityTimeout = 10 * time.Second

// dialWithTimeout bounds dial's own runtime to timeout. This matters
// because DialTunnel's stream open (yamux Session.Open()) takes no context
// or deadline of its own — on a session that has hit yamux's in-flight-SYN
// limit, Open() can block until the session closes, well past the
// deadline probeHostname sets on the returned conn (which only starts
// after dial returns). Without this wrapper a single wedged tunnel could
// stall the whole reachability sweep indefinitely despite the advertised
// per-hostname timeout.
//
// If dial is still running when timeout fires, it's left to finish in the
// background; if it eventually succeeds, the resulting conn is closed
// immediately rather than left dangling.
func dialWithTimeout(dial func(spotID string, port int) (net.Conn, error), spotID string, port int, timeout time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := dial(spotID, port)
		ch <- result{conn, err}
	}()

	select {
	case res := <-ch:
		return res.conn, res.err
	case <-time.After(timeout):
		go func() {
			if res := <-ch; res.conn != nil {
				_ = res.conn.Close()
			}
		}()
		return nil, fmt.Errorf("timed out after %s opening a stream to spot %q", timeout, spotID)
	}
}

// probeHostname dials spotID's port over the tunnel via dial, completes a
// TLS handshake presenting hostname as SNI, then sends a bare HTTP/1.1 GET
// and inspects the status line.
//
// InsecureSkipVerify is correct here, not a weakening: this probe runs from
// the sentinel's own trusted vantage point to check liveness, the same
// rationale as selfCheckProxyPath's certificate skip.
//
// A 502 is treated as unreachable. That's the exact failure #1872
// documented: the sentinel's SNI router successfully dials the tunnel and
// reaches a real HTTP server, but that server (the primary's own Caddy)
// has no route configured for this hostname and answers with Caddy's own
// upstream-unavailable page. A registered-but-502ing hostname is worse
// than a registered-but-unreachable one, because it looks alive in
// GET /sentinel/primaries — that false confidence is what this probe
// exists to remove.
func probeHostname(dial func(spotID string, port int) (net.Conn, error), spotID string, port int, hostname string, timeout time.Duration) AliasHealth {
	health := AliasHealth{Hostname: hostname, CheckedAt: time.Now()}

	conn, err := dialWithTimeout(dial, spotID, port, timeout)
	if err != nil {
		health.Error = fmt.Sprintf("dial: %v", err)
		return health
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         hostname,
		InsecureSkipVerify: true, // #nosec G402 -- liveness probe, see doc comment above
	})
	if err := tlsConn.Handshake(); err != nil {
		health.Error = fmt.Sprintf("tls handshake: %v", err)
		return health
	}

	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nUser-Agent: containarium-sentinel-reachability\r\nConnection: close\r\n\r\n", hostname)
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		health.Error = fmt.Sprintf("write request: %v", err)
		return health
	}

	statusLine, err := bufio.NewReader(tlsConn).ReadString('\n')
	if err != nil {
		health.Error = fmt.Sprintf("read response: %v", err)
		return health
	}
	code, err := parseHTTPStatusCode(statusLine)
