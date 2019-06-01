package wake

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/app"
)

// fakeProxyManager satisfies ProxyManager for tests. Records every call
// in order and optionally returns a pre-canned error from updateErr (or
// grpcUpdateErr) on each invocation.
type fakeProxyManager struct {
	mu            sync.Mutex
	updateCalls   []proxyCall
	grpcCalls     []proxyCall
	updateErr     error // returned by UpdateRoute
	grpcUpdateErr error // returned by UpdateGRPCRoute
}

type proxyCall struct {
	subdomain string
	ip        string
	port      int
}

func (f *fakeProxyManager) UpdateRoute(subdomain, ip string, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls = append(f.updateCalls, proxyCall{subdomain, ip, port})
	return f.updateErr
}

func (f *fakeProxyManager) UpdateGRPCRoute(subdomain, ip string, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grpcCalls = append(f.grpcCalls, proxyCall{subdomain, ip, port})
	return f.grpcUpdateErr
}

func (f *fakeProxyManager) httpCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updateCalls)
}

func (f *fakeProxyManager) grpcCallsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.grpcCalls)
}

// httpRoute builds a RouteRecord for an HTTP route.
func httpRoute(subdomain, ip string, port int, container string) *app.RouteRecord {
	return &app.RouteRecord{
		FullDomain:    subdomain,
		Subdomain:     subdomain,
		TargetIP:      ip,
		TargetPort:    port,
		Protocol:      string(app.RouteProtocolHTTP),
		ContainerName: container,
	}
}

func grpcRoute(subdomain, ip string, port int, container string) *app.RouteRecord {
	return &app.RouteRecord{
		FullDomain:    subdomain,
		Subdomain:     subdomain,
		TargetIP:      ip,
		TargetPort:    port,
		Protocol:      string(app.RouteProtocolGRPC),
		ContainerName: container,
	}
}

// TestRouter_SwapToWake_SingleRoute — single HTTP route flips to the
// wake address and the tracker is marked once.
func TestRouter_SwapToWake_SingleRoute(t *testing.T) {
	pm := &fakeProxyManager{}
	tracker := New()
	r := NewRouter(pm, tracker, "10.0.3.1", 8080)

	routes := []*app.RouteRecord{httpRoute("alice.example.test", "10.0.3.42", 9000, "alice-container")}
	if err := r.SwapToWake(context.Background(), "alice-container", routes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := pm.httpCallsCount(); got != 1 {
		t.Fatalf("UpdateRoute calls = %d, want 1", got)
	}
	if c := pm.updateCalls[0]; c.subdomain != "alice.example.test" || c.ip != "10.0.3.1" || c.port != 8080 {
		t.Errorf("UpdateRoute args = %+v, want subdomain=alice.example.test ip=10.0.3.1 port=8080", c)
	}
	if host, port, ok := tracker.IsInWakeMode("alice-container"); !ok || host != "10.0.3.1" || port != 8080 {
		t.Errorf("tracker entry = (%q,%d,%v), want (10.0.3.1,8080,true)", host, port, ok)
	}
}

// TestRouter_SwapToWake_MultiRoute — three routes → three UpdateRoute
// calls; all share the same tracker entry (keyed by container name).
func TestRouter_SwapToWake_MultiRoute(t *testing.T) {
	pm := &fakeProxyManager{}
	tracker := New()
	r := NewRouter(pm, tracker, "10.0.3.1", 8080)

	routes := []*app.RouteRecord{
		httpRoute("a.example.test", "10.0.3.42", 9000, "alice-container"),
		httpRoute("b.example.test", "10.0.3.42", 9001, "alice-container"),
		httpRoute("c.example.test", "10.0.3.42", 9002, "alice-container"),
	}
	if err := r.SwapToWake(context.Background(), "alice-container", routes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := pm.httpCallsCount(); got != 3 {
		t.Fatalf("UpdateRoute calls = %d, want 3", got)
	}
	if got := len(tracker.Snapshot()); got != 1 {
		t.Errorf("tracker entries = %d, want 1 (one per container, not per route)", got)
	}
}

// TestRouter_SwapToWake_NoRoutes — empty/nil slice is a no-op, the
// tracker stays empty and no error is returned.
func TestRouter_SwapToWake_NoRoutes(t *testing.T) {
	pm := &fakeProxyManager{}
	tracker := New()
