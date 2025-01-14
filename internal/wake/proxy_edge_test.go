package wake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/app"
)

// countingStarter is a WakeStarter that records every WakeForRequest
// invocation atomically. The configured response is returned for every
// call.
type countingStarter struct {
	calls atomic.Int64
	delay time.Duration
	ready bool
	ip    string
	port  int
	err   error
}

func (c *countingStarter) WakeForRequest(ctx context.Context, username string) (bool, string, int, error) {
	c.calls.Add(1)
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return false, "", 0, ctx.Err()
		}
	}
	return c.ready, c.ip, c.port, c.err
}

// recordingAudit captures the last (or all) Log calls so tests can
// assert on event name + fields.
type recordingAudit struct {
	mu     sync.Mutex
	events []auditEvent
}

type auditEvent struct {
	name   string
	fields map[string]any
}

func (a *recordingAudit) Log(event string, fields map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Deep-copy the fields map so a later mutation by the impl can't
	// race with the test reader.
	copied := make(map[string]any, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	a.events = append(a.events, auditEvent{name: event, fields: copied})
}

func (a *recordingAudit) last() (auditEvent, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) == 0 {
		return auditEvent{}, false
	}
	return a.events[len(a.events)-1], true
}

// upstreamWithRecorder spins up an httptest server that records the
// last (path, host, headers) it received and replies with `body`.
type upstreamRecorder struct {
	mu          sync.Mutex
	lastPath    string
	lastHost    string
	lastHeaders http.Header
	upgrade     bool
}

func startUpstream(t *testing.T, body string) (string, int, *upstreamRecorder, *httptest.Server) {
	t.Helper()
	rec := &upstreamRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.lastPath = r.URL.Path
		rec.lastHost = r.Host
		rec.lastHeaders = r.Header.Clone()
		rec.upgrade = strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port parse: %v", err)
	}
	return host, port, rec, srv
}

// TestWakeProxy_StarterReturnsNotReady — starter reports ready=false →
// proxy responds 503 with Retry-After: 5.
func TestWakeProxy_StarterReturnsNotReady(t *testing.T) {
	proxy := NewWakeProxy(
		&countingStarter{ready: false, ip: "10.0.0.42", port: 8080},
		&fakeLookup{fullDomain: "alice.example.test", containerName: "alice-container", targetIP: "10.0.0.42", targetPort: 8080},
		&fakeRouteStore{},
		nil,
		nil,
		1*time.Second,
	)
	req := httptest.NewRequest(http.MethodGet, "http://alice.example.test/", nil)
	req.Host = "alice.example.test"
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q", got, "5")
	}
}

// TestWakeProxy_StarterReturnsError — hard error from starter → 503.
func TestWakeProxy_StarterReturnsError(t *testing.T) {
	proxy := NewWakeProxy(
		&countingStarter{err: errors.New("incus start failed")},
		&fakeLookup{fullDomain: "alice.example.test", containerName: "alice-container"},
		&fakeRouteStore{},
		nil,
		nil,
		1*time.Second,
	)
	req := httptest.NewRequest(http.MethodGet, "http://alice.example.test/", nil)
	req.Host = "alice.example.test"
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestWakeProxy_InflightDedup — 50 concurrent requests for the same
// container coalesce into a single starter call. All 50 receive the
// upstream body. Run with -race to also exercise the inflight map.
func TestWakeProxy_InflightDedup(t *testing.T) {
	host, port, _, srv := startUpstream(t, "hello")
	defer srv.Close()

	const fullDomain = "alice.example.test"
	starter := &countingStarter{ready: true, ip: host, port: port, delay: 50 * time.Millisecond}
	proxy := NewWakeProxy(
		starter,
		&fakeLookup{fullDomain: fullDomain, containerName: "alice-container", targetIP: host, targetPort: port},
		&fakeRouteStore{},
		nil,
		nil,
		5*time.Second,
	)

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	results := make(chan int, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://"+fullDomain+"/", nil)
			req.Host = fullDomain
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			results <- rec.Code
		}()
	}
	wg.Wait()
	close(results)

	var ok200 int
	for code := range results {
		if code == http.StatusOK {
			ok200++
		}
	}
	if ok200 != N {
		t.Errorf("got %d/%d 200s", ok200, N)
	}
	if got := starter.calls.Load(); got != 1 {
		t.Errorf("starter calls = %d, want 1 (inflight de-dup failed)", got)
	}
}

// TestWakeProxy_InflightDedup_DifferentContainers — two different
// containers in flight at the same time → two starter calls, not one.
// The dedup key is the container name.
func TestWakeProxy_InflightDedup_DifferentContainers(t *testing.T) {
	host, port, _, srv := startUpstream(t, "hi")
	defer srv.Close()

	starter := &countingStarter{ready: true, ip: host, port: port, delay: 30 * time.Millisecond}

	// Two routes / two containers, served by a lookup that resolves on
	// FullDomain.
	stubs := []lookupStub{
		{"alice.example.test", "alice-container", host, port},
		{"bob.example.test", "bob-container", host, port},
	}
	lookup := &multiLookup{stubs: stubs}

	proxy := NewWakeProxy(starter, lookup, &fakeRouteStore{}, nil, nil, 5*time.Second)

	var wg sync.WaitGroup
	for _, s := range stubs {
		s := s
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "http://"+s.full+"/", nil)
				req.Host = s.full
				rec := httptest.NewRecorder()
				proxy.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("%s: status %d, want 200", s.full, rec.Code)
				}
			}()
		}
	}
	wg.Wait()

	if got := starter.calls.Load(); got != 2 {
		t.Errorf("starter calls = %d, want 2 (one per container)", got)
	}
}

// lookupStub describes one route for multiLookup. Named so the test
// callers can construct the slice literal without redeclaring the
// anonymous struct each time (Go has no structural equivalence between
// independently-declared anon structs across statements).
type lookupStub struct {
	full string
	name string
	ip   string
	port int
}

// multiLookup resolves multiple FullDomains in one struct.
type multiLookup struct {
	stubs []lookupStub
}

func (m *multiLookup) ResolveByHost(ctx context.Context, host string) (*app.RouteRecord, bool, error) {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	for _, s := range m.stubs {
		if s.full == host {
			return &app.RouteRecord{
				FullDomain:    s.full,
				Subdomain:     s.full,
				ContainerName: s.name,
				TargetIP:      s.ip,
				TargetPort:    s.port,
				Protocol:      "http",
			}, true, nil
		}
	}
	return nil, false, nil
}

// TestWakeProxy_PathStripping — /wake/foo/bar is the smoke-test prefix;
// the handler strips it and forwards /foo/bar upstream.
func TestWakeProxy_PathStripping(t *testing.T) {
	host, port, rec, srv := startUpstream(t, "ok")
	defer srv.Close()

	proxy := NewWakeProxy(
		&countingStarter{ready: true, ip: host, port: port},
		&fakeLookup{fullDomain: "alice.example.test", containerName: "alice-container", targetIP: host, targetPort: port},
		&fakeRouteStore{},
		nil,
		nil,
		5*time.Second,
	)
	req := httptest.NewRequest(http.MethodGet, "http://alice.example.test/wake/foo/bar", nil)
	req.Host = "alice.example.test"
	resp := httptest.NewRecorder()
	proxy.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", resp.Code, resp.Body.String())
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.lastPath != "/foo/bar" {
		t.Errorf("upstream saw path %q, want /foo/bar", rec.lastPath)
	}
