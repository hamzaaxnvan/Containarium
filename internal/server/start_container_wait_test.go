package server

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// newStartContainerTestServer wires a ContainerServer with just enough
// dependencies for StartContainer to run end-to-end: a mocked backend
// + the real Manager + a no-op emitter. routeStore is intentionally
// nil so waitForContainerReady takes its short-circuit fast-path,
// which lets the test reason about wall-clock without a TCP listener.
func newStartContainerTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) *ContainerServer {
	t.Helper()
	mock := incustest.NewMockBackend()
	for n, info := range seed {
		mock.Containers[n] = info
	}
	return &ContainerServer{
		manager: container.NewWithBackend(mock),
		emitter: events.NewEmitter(events.NewBus()),
	}
}

// TestStartContainer_NoWaitDefault — WaitForReady defaults to false;
// the handler must complete near-instantly without consulting the
// probe.
func TestStartContainer_NoWaitDefault(t *testing.T) {
	s := newStartContainerTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Stopped", IPAddress: "10.0.0.42"},
	})
	start := time.Now()
	resp, err := s.StartContainer(testCtx(), &pb.StartContainerRequest{Username: "alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReadyTimedOut {
		t.Errorf("default (no wait) must not report timeout")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("no-wait path took %v, expected near-instant", elapsed)
	}
}

// TestStartContainer_WaitForReady_NilRouteStoreShortCircuits —
// wait_for_ready=true with a nil routeStore must still be near-
// instant (the probe short-circuits to "ready"). Prevents accidental
// deadlock on daemons that lack --app-hosting.
func TestStartContainer_WaitForReady_NilRouteStoreShortCircuits(t *testing.T) {
