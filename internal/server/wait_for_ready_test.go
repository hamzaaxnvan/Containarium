package server

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/app"
)

type fakeRouteLister struct {
	routes []*app.RouteRecord
	err    error
}

func (f *fakeRouteLister) ListByContainer(ctx context.Context, name string) ([]*app.RouteRecord, error) {
	return f.routes, f.err
}

func (f *fakeRouteLister) Delete(ctx context.Context, fullDomain string) error { return nil }

func (f *fakeRouteLister) Save(ctx context.Context, r *app.RouteRecord) error { return nil }

func listenLocal(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return port
}

// TestWaitForContainerReady_NilRouteStore — when the daemon was
// started without --app-hosting, routeStore is nil and the probe must
// short-circuit to "ready" (false = not timed out). Anything else
// would block start_container against daemons with no route store.
func TestWaitForContainerReady_NilRouteStore(t *testing.T) {
	s := &ContainerServer{} // routeStore deliberately nil
	start := time.Now()
	timedOut := s.waitForContainerReady(context.Background(), "alice", "10.0.0.1", 5*time.Second)
	if timedOut {
		t.Errorf("nil routeStore should short-circuit to ready, got timedOut=true")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("nil routeStore should return immediately, took %v", elapsed)
	}
}
