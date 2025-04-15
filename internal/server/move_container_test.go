package server

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeRunner records every Incus call the orchestrator makes and lets
// each test seed deterministic outcomes. Unit tests assert on the call
// sequence (the orchestration logic) rather than on real Incus
// behavior — those integration concerns live in a separate harness
// that needs two real VMs.
type fakeRunner struct {
	mu              sync.Mutex
	calls           []string
	failSnapshot    error
	failCopyInit    error
	failCopyRefresh map[int]error // keyed by call count: first call is 0
	copyRefreshN    int
	failStop        error
	failStart       error
	hasRemote       bool
}

// copyDesc mirrors what the real runner does with the pool: `--storage` is
// appended only when one is given, so an unencrypted migration's command is
// byte-identical to what it always was.
func copyDesc(container, snap, remote, pool string) string {
	desc := fmt.Sprintf("%s/%s -> %s", container, snap, remote)
	if pool != "" {
		desc += " storage=" + pool
	}
	return desc
}

func (f *fakeRunner) log(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeRunner) Snapshot(c, s string) error {
	f.log(fmt.Sprintf("snapshot %s/%s", c, s))
	return f.failSnapshot
}
func (f *fakeRunner) DeleteSnapshot(c, s string) error {
	f.log(fmt.Sprintf("delsnap %s/%s", c, s))
	return nil
}
func (f *fakeRunner) CopyInitial(c, s, r, pool string) error {
	f.log("copyinit " + copyDesc(c, s, r, pool))
	return f.failCopyInit
}
func (f *fakeRunner) CopyRefresh(c, s, r, pool string) error {
	f.log("refresh " + copyDesc(c, s, r, pool))
	n := f.copyRefreshN
	f.copyRefreshN++
	if err, ok := f.failCopyRefresh[n]; ok {
		return err
	}
	return nil
}
func (f *fakeRunner) Stop(c string) error {
	f.log(fmt.Sprintf("stop %s", c))
	return f.failStop
}
func (f *fakeRunner) Start(c string) error {
	f.log(fmt.Sprintf("start %s", c))
	return f.failStart
}
func (f *fakeRunner) HasRemote(r string) (bool, error) {
	return f.hasRemote, nil
}

// newTestContainerServer builds a ContainerServer with just enough
// wiring for the move orchestration tests: a peer pool with the local
// + one fake target, no route store (the orchestrator handles
// routeStore==nil gracefully), and the fake runner.
func newTestContainerServer(t *testing.T, runner incus.MigrationRunner, targetID string) *ContainerServer {
	t.Helper()
	cs := &ContainerServer{}
	pp := NewPeerPool("local-vm", "", nil, "")
	// Inject the target as a fake peer with a stub address; ForwardRequest
	// would normally call out, but we override that path via the
	// fakeAdopt hook below.
	pp.peers[targetID] = &PeerClient{ID: targetID, Addr: "stub:8080", Healthy: true}
	cs.peerPool = pp
	cs.moveRunner = runner
	return cs
}

// TestMoveContainer_HappyPath exercises the three-phase orchestration
// end to end without errors. Asserts on:
//   - the exact call sequence (3-phase + cutover stop, final copy, adopt)
//   - default iteration count (3) → 1 init + 3 deltas + 1 final = 5 incus copies
//   - new IP returned matches what AdoptMigratedContainer says
func TestMoveContainer_HappyPath(t *testing.T) {
	runner := &fakeRunner{hasRemote: true}
	cs := newTestContainerServer(t, runner, "vm2")

	// Stub the adopt RPC: return a fixed new IP without going over the wire.
	prev := overrideAdoptForTest(func(peer *PeerClient, _ string, _ *pb.AdoptMigratedContainerRequest) (*pb.AdoptMigratedContainerResponse, error) {
		return &pb.AdoptMigratedContainerResponse{NewIpAddress: "10.0.7.42"}, nil
	})
	defer prev()

	req := &pb.MoveContainerRequest{
		Username:        "alice",
		TargetBackendId: "vm2",
		// Default max_iterations (3), default delta_threshold (5).
		// Each iteration in the fake runner takes ~0ms, well under
		// the threshold, so the loop should exit after iter 1.
		// 1 init + 1 delta-loop iter + 1 final = 3 incus copies.
		// We also have 3 snapshots (sync0, sync1, final) + their
		// deletes at the end (sync0, sync1, final = 3).
	}
	resp, err := cs.MoveContainer(testCtx(), req)
	if err != nil {
		t.Fatalf("MoveContainer err = %v", err)
	}

	if resp.NewIpAddress != "10.0.7.42" {
		t.Errorf("NewIpAddress = %q, want %q", resp.NewIpAddress, "10.0.7.42")
	}
