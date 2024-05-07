package server

import (
	"context"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sandbox/ipam"
	"github.com/footprintai/containarium/internal/sandbox/pool"
	"github.com/footprintai/containarium/internal/ttlsweeper"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestSpawnSandbox_StampsTTLExpiresAt pins that the cold path writes the
// exact key/format ttlsweeper.Decide reads — the sandbox equivalent of
// stampTTL's own contract (container_server_ttl.go). Without this the
// adapter would never see a TTL to act on and every sandbox would leak
// forever, silently.
func TestSpawnSandbox_StampsTTLExpiresAt(t *testing.T) {
	backend := newFakeSandboxBackend()
	s := NewSandboxServer(backend, nil)

	before := time.Now()
	sb := spawnAsAlice(t, backend, s)
	after := time.Now()

	info := backend.instances[sb.SandboxId]
	if info.TTLExpiresAt.IsZero() {
		t.Fatal("TTLExpiresAt not stamped on the cold path")
	}
	// +/- 1s slack: RFC3339 (no sub-second field) truncates the stamped
	// value to whole seconds, so it can legitimately land a fraction of a
	// second before `before`'s own sub-second reading.
	wantMin := before.Add(defaultSandboxIdleTTL).Add(-time.Second)
	wantMax := after.Add(defaultSandboxIdleTTL).Add(time.Second)
	if info.TTLExpiresAt.Before(wantMin) || info.TTLExpiresAt.After(wantMax) {
		t.Errorf("TTLExpiresAt = %v, want between %v and %v (now + default idle ttl)", info.TTLExpiresAt, wantMin, wantMax)
	}
}

// TestSpawnSandbox_ClaimFromPool_StampsTTLExpiresAt is
// TestSpawnSandbox_StampsTTLExpiresAt's sibling for the pool-claim path —
// claimFromPool sets the same key via a separate SetConfig call, and the
// two constructions must agree.
func TestSpawnSandbox_ClaimFromPool_StampsTTLExpiresAt(t *testing.T) {
	backend := newFakeSandboxBackend()
	allocator, err := ipam.New("10.100.0.10", "10.100.0.250", "10.100.0.1", 0)
	if err != nil {
		t.Fatalf("ipam.New: %v", err)
	}
	p := pool.New(backend, allocator, pool.Config{
		MinWarm:    map[pb.SandboxTemplate]int{pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE: 1},
		Image:      map[pb.SandboxTemplate]string{pb.SandboxTemplate_SANDBOX_TEMPLATE_BASE: "images:ubuntu/24.04"},
		NICNetwork: "incusbr0",
	})
	p.Reconcile(context.Background())
	s := NewSandboxServer(backend, p)

	resp, err := s.SpawnSandbox(ctxAs("alice", false), &pb.SpawnSandboxRequest{})
	if err != nil {
		t.Fatalf("SpawnSandbox: %v", err)
	}

	info := backend.instances[resp.Sandbox.SandboxId]
	if info.TTLExpiresAt.IsZero() {
		t.Fatal("TTLExpiresAt not stamped on the pool-claim path")
	}
}

// TestTTLSweeperSandboxAdapter_ListContainers_FiltersToSandboxesOnly pins
