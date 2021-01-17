package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeWakeRouter records SwapToWake / SwapToDirect calls. Both methods
// satisfy the WakeRouter interface defined in wake_router.go.
type fakeWakeRouter struct {
	mu              sync.Mutex
	swapToWake      []wakeCall
	swapToDirect    []wakeCall
	swapToWakeErr   error
	swapToDirectErr error
}

type wakeCall struct {
	container string
	routes    []*app.RouteRecord
}

func (f *fakeWakeRouter) SwapToWake(ctx context.Context, name string, routes []*app.RouteRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.swapToWake = append(f.swapToWake, wakeCall{name, routes})
	return f.swapToWakeErr
}

func (f *fakeWakeRouter) SwapToDirect(ctx context.Context, name string, routes []*app.RouteRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.swapToDirect = append(f.swapToDirect, wakeCall{name, routes})
	return f.swapToDirectErr
}

func (f *fakeWakeRouter) wakeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.swapToWake)
}

func (f *fakeWakeRouter) directCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.swapToDirect)
}

// memoryRouteStore is an in-memory routeLister for the hook tests.
type memoryRouteStore struct {
	routes []*app.RouteRecord
	err    error
}

func (m *memoryRouteStore) ListByContainer(ctx context.Context, name string) ([]*app.RouteRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	var out []*app.RouteRecord
	for _, r := range m.routes {
		if r.ContainerName == name {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memoryRouteStore) Delete(ctx context.Context, fullDomain string) error { return nil }
func (m *memoryRouteStore) Save(ctx context.Context, route *app.RouteRecord) error {
	m.routes = append(m.routes, route)
	return nil
}

// newWakeHookTestServer wires a ContainerServer with a mock backend, a
// real Manager, an in-memory route store, and a configurable wake
// router. seed populates the mock with initial container state.
func newWakeHookTestServer(t *testing.T, seed map[string]*incus.ContainerInfo, routes []*app.RouteRecord, router WakeRouter) (*ContainerServer, *incustest.MockBackend) {
	t.Helper()
	mock := incustest.NewMockBackend()
	for n, info := range seed {
		mock.Containers[n] = info
	}
	s := &ContainerServer{
		manager:    container.NewWithBackend(mock),
		emitter:    events.NewEmitter(events.NewBus()),
		routeStore: &memoryRouteStore{routes: routes},
		wakeRouter: router,
	}
	return s, mock
}

// TestStopForAutoSleep_CallsSwapToWake — happy path: stop + a wired
// router + matching routes → exactly one SwapToWake call with the
// container's routes.
func TestStopForAutoSleep_CallsSwapToWake(t *testing.T) {
	router := &fakeWakeRouter{}
	routes := []*app.RouteRecord{{
		ContainerName: "alice-container",
		FullDomain:    "alice.example.test",
		TargetIP:      "10.0.0.42",
		TargetPort:    8080,
		Protocol:      "http",
	}}
	s, _ := newWakeHookTestServer(t,
		map[string]*incus.ContainerInfo{"alice-container": {Name: "alice-container", State: "Running"}},
		routes, router)

	if err := s.StopForAutoSleep(testCtx(), "alice", "idle 90m", 90); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := router.wakeCount(); got != 1 {
		t.Fatalf("SwapToWake calls = %d, want 1", got)
	}
	call := router.swapToWake[0]
	if call.container != "alice-container" {
		t.Errorf("SwapToWake container = %q, want alice-container", call.container)
	}
	if len(call.routes) != 1 || call.routes[0].FullDomain != "alice.example.test" {
		t.Errorf("SwapToWake routes = %+v, want one route for alice.example.test", call.routes)
	}
}

// TestStopForAutoSleep_NilWakeRouter_NoOp — without a wakeRouter the
// stop still succeeds (backward compat with --app-hosting=off).
func TestStopForAutoSleep_NilWakeRouter_NoOp(t *testing.T) {
	s, _ := newWakeHookTestServer(t,
		map[string]*incus.ContainerInfo{"alice-container": {Name: "alice-container", State: "Running"}},
		nil, nil) // nil router

	if err := s.StopForAutoSleep(testCtx(), "alice", "idle 90m", 90); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStopForAutoSleep_SwapToWakeError_DoesNotFailStop — if SwapToWake
// returns an error, StopForAutoSleep still returns success (the stop
// itself already happened; RouteSync will reconverge).
func TestStopForAutoSleep_SwapToWakeError_DoesNotFailStop(t *testing.T) {
	router := &fakeWakeRouter{swapToWakeErr: errors.New("caddy 502")}
	routes := []*app.RouteRecord{{ContainerName: "alice-container", FullDomain: "alice.example.test", TargetIP: "10.0.0.42", TargetPort: 8080, Protocol: "http"}}
	s, _ := newWakeHookTestServer(t,
		map[string]*incus.ContainerInfo{"alice-container": {Name: "alice-container", State: "Running"}},
		routes, router)

	if err := s.StopForAutoSleep(testCtx(), "alice", "idle 90m", 90); err != nil {
		t.Fatalf("SwapToWake failure must not fail the stop: got %v", err)
	}
}

// TestStopForAutoSleep_RouteStoreError_DoesNotFailStop — listing
// routes can fail (Postgres hiccup); the stop still succeeds.
func TestStopForAutoSleep_RouteStoreError_DoesNotFailStop(t *testing.T) {
	router := &fakeWakeRouter{}
	s := &ContainerServer{
		manager:    container.NewWithBackend(seededMock("alice-container", "Running")),
		emitter:    events.NewEmitter(events.NewBus()),
		routeStore: &memoryRouteStore{err: errors.New("pg unreachable")},
		wakeRouter: router,
	}
	if err := s.StopForAutoSleep(testCtx(), "alice", "idle 90m", 90); err != nil {
