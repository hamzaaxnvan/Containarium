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
