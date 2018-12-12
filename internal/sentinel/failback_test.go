package sentinel

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a bytes.Buffer safe for concurrent use. The standard logger is
// process-global, so a goroutine that outlives an earlier test (a tunnel
// session monitor, say) can still log into a buffer a later test installed with
// log.SetOutput; the logger serialises its own writes but not the test's reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// failbackRig is a Manager in proxy mode with a preferred primary ("gcp",
// priority 0) and a lower-priority tunnel peer, whose health the test scripts
// tick by tick. switchToProxy is stubbed through its seam, but the stub applies
// the one effect everything else depends on: it makes the backend the primary.
type failbackRig struct {
	t     *testing.T
	m     *Manager
	gcp   *Backend
	peer  *Backend
	up    map[string]bool
	mu    sync.Mutex
	calls []string // IDs passed to switchToProxy, in order
}

func newFailbackRig(t *testing.T, failbackChecks int) *failbackRig {
	t.Helper()
	r := &failbackRig{t: t, up: map[string]bool{"10.0.0.1": true, "10.0.0.2": true}}
	r.m = NewManager(Config{HealthyThreshold: 2, UnhealthyThreshold: 2, FailbackChecks: failbackChecks}, &fakeRecoveryProvider{})
	r.gcp = &Backend{ID: "gcp", IP: "10.0.0.1", Healthy: true, Priority: 0}
	r.peer = &Backend{ID: "tunnel-a", IP: "10.0.0.2", Healthy: true, Priority: 10}
	r.m.backends.Add(r.gcp)
	r.m.backends.Add(r.peer)
	r.m.checkHealthFn = func(ip string) bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.up[ip]
	}
	r.m.switchToProxyFn = func(b *Backend) error {
		r.mu.Lock()
		r.calls = append(r.calls, b.ID)
		r.mu.Unlock()
		r.m.mu.Lock()
		r.m.primary = b
		r.m.mu.Unlock()
		return nil
	}
	r.m.state.Store(StateProxy)
	r.m.primary = r.gcp
	return r
}

func (r *failbackRig) set(ip string, healthy bool) {
	r.mu.Lock()
	r.up[ip] = healthy
	r.mu.Unlock()
}

// tick runs one health-check round n times.
func (r *failbackRig) tick(n int) {
	for i := 0; i < n; i++ {
		r.m.healthCheckAll(context.Background())
	}
}

func (r *failbackRig) switches() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *failbackRig) primaryID() string {
	r.m.mu.RLock()
	defer r.m.mu.RUnlock()
	if r.m.primary == nil {
		return ""
	}
	return r.m.primary.ID
}

// The production sequence (#2476): the primary blips, the sentinel fails over to
// a tunnel peer that cannot serve its hostnames, the primary comes back, and the
// sentinel used to stay on the peer until restarted.
func TestFailback_RestoresThePreferredPrimaryAfterItRecovers(t *testing.T) {
	r := newFailbackRig(t, 4)

	r.set("10.0.0.1", false)
	r.tick(2) // UnhealthyThreshold = 2
	require.Equal(t, []string{"tunnel-a"}, r.switches(), "precondition: failed over to the peer")
	require.Equal(t, "tunnel-a", r.primaryID())
	assert.False(t, r.m.primaryIsPreferred(), "sitting on the fallback is not the preferred primary")

	r.set("10.0.0.1", true)
	r.tick(3) // healthy again, but not yet stable for FailbackChecks
