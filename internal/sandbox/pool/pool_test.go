package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sandbox/ipam"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBackend is the pool.Backend test double — the "Incus layer" the
// design note's own test list calls for, fast and deterministic, no host.
type fakeBackend struct {
	mu sync.Mutex

	created []string
	started []string
	stopped []string
	deleted []string

	// createErr, when set, is returned by CreateContainer for names
	// matching failFor (empty failFor = fail every create).
	createErr error
	failFor   map[string]bool // nil = fail everything if createErr != nil

	// gate, if non-nil, blocks CreateContainer until closed — lets a test
	// observe a member mid-warm before it becomes ready.
	gate chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{}
}

func (b *fakeBackend) CreateContainer(config incus.ContainerConfig) error {
	if b.gate != nil {
		<-b.gate
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.createErr != nil && (b.failFor == nil || b.failFor[config.Name]) {
		return b.createErr
	}
	b.created = append(b.created, config.Name)
	return nil
}

func (b *fakeBackend) StartContainer(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.started = append(b.started, name)
	return nil
}

func (b *fakeBackend) StopContainer(name string, _ bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = append(b.stopped, name)
	return nil
}

func (b *fakeBackend) DeleteContainer(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deleted = append(b.deleted, name)
	return nil
}

func (b *fakeBackend) deleteCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.deleted)
}

func testAllocator(t *testing.T) *ipam.Allocator {
	t.Helper()
	a, err := ipam.New("10.100.0.10", "10.100.0.250", "10.100.0.1", 0)
	if err != nil {
		t.Fatalf("ipam.New: %v", err)
	}
	return a
}

func testConfig(minWarm int) Config {
	return Config{
		MinWarm:    map[pb.SandboxTemplate]int{pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE: minWarm},
		Image:      map[pb.SandboxTemplate]string{pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE: "images:ubuntu/24.04"},
		NICNetwork: "incusbr0",
	}
}

// ---- Claim -----------------------------------------------------------

func TestClaim_EmptyPoolReturnsErrPoolExhausted(t *testing.T) {
	p := New(newFakeBackend(), testAllocator(t), testConfig(0))

	_, err := p.Claim(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE)
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("Claim on empty pool = %v, want ErrPoolExhausted", err)
	}
}

func TestClaim_OneReadyMemberClaimedAndRemoved(t *testing.T) {
	backend := newFakeBackend()
	p := New(backend, testAllocator(t), testConfig(1))

	p.Reconcile(context.Background())
	if got := p.ReadyCount(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE); got != 1 {
		t.Fatalf("ReadyCount after warming to 1 = %d, want 1", got)
	}

	m, err := p.Claim(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if m.ID == "" || m.IP == "" {
		t.Errorf("claimed member incomplete: %+v", m)
	}
	if got := p.ReadyCount(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE); got != 0 {
		t.Errorf("ReadyCount after claiming the only member = %d, want 0", got)
	}

	if _, err := p.Claim(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE); !errors.Is(err, ErrPoolExhausted) {
		t.Errorf("second Claim = %v, want ErrPoolExhausted", err)
	}
}

// TestClaim_ConcurrentClaimsEachMemberExactlyOnce pins the design note's
// own test requirement, run under -race with 100 goroutines against a
// pool of exactly 100 ready members.
func TestClaim_ConcurrentClaimsEachMemberExactlyOnce(t *testing.T) {
	const n = 100
	backend := newFakeBackend()
	p := New(backend, testAllocator(t), testConfig(n))
	p.Reconcile(context.Background())
	if got := p.ReadyCount(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE); got != n {
		t.Fatalf("ReadyCount after warming = %d, want %d", got, n)
	}

	var wg sync.WaitGroup
	claimed := make([]*Member, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claimed[i], errs[i] = p.Claim(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE)
		}(i)
	}
	wg.Wait()

	seen := make(map[string]int, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		seen[claimed[i].ID]++
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct members claimed, want %d", len(seen), n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("member %s claimed %d times", id, count)
		}
	}
	if got := p.ReadyCount(pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE); got != 0 {
		t.Errorf("ReadyCount after claiming everything = %d, want 0", got)
	}
}

// TestClaimDuringReconcile_NoTornState hammers Claim and Reconcile
// concurrently (run with -race) — the requirement is no data race and no
// double-claim, not any particular interleaving outcome.
func TestClaimDuringReconcile_NoTornState(t *testing.T) {
	backend := newFakeBackend()
	// A generous range: this test's claim loop never Releases what it
	// claims (by design — it's only checking for torn state, not
	// exercising the release path), so claimed addresses are gone for
