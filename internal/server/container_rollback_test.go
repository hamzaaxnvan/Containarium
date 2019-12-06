package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Container snapshot rollback (#1160b).
//
// Rollback is destructive twice over: everything written since the snapshot
// is discarded, and any snapshot taken after it has to be destroyed too. So
// nearly all of this file is about what the RPC REFUSES, and each refusal is
// asserted to have also not run the destructive command — a guard that
// returns an error after already rolling back is not a guard.
//
// The third refusal is #1202's second acceptance criterion: rolling back a
// container whose encryption key is unavailable produces a dataset nobody can
// read. `zfscrypt.EnsureInspectable` gives that a specific error instead of
// whatever ZFS says when a read hits an unkeyed dataset, which reads like
// corruption and sends an operator hunting the wrong problem.

// stubBoxes reports a fixed state and records whether Stop was called, so the
// running-container guard can be tested without a container runtime.
type stubBoxes struct {
	box.BoxBackend
	state    pb.ContainerState
	getErr   error
	stopped  []string
	stopErr  error
	stopSeen func()
}

func (s *stubBoxes) Get(_ context.Context, ref box.BoxRef) (*box.BoxStatus, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &box.BoxStatus{Ref: ref, State: s.state}, nil
}

func (s *stubBoxes) Stop(_ context.Context, ref box.BoxRef, _ bool) error {
	if s.stopSeen != nil {
		s.stopSeen()
	}
	if s.stopErr != nil {
		return s.stopErr
	}
	s.stopped = append(s.stopped, ref.Tenant)
	return nil
}

// rollbackFixture wires a server whose snapshots run against a fake `zfs` and
// whose box backend reports the given state.
func rollbackFixture(t *testing.T, z *zfsFake, boxes *stubBoxes) *ContainerServer {
	t.Helper()
	return &ContainerServer{
		boxBackend: boxes,
		snapshots: &snapshotOps{
			zfs: zfscrypt.NewManager(z),
			datasetFor: func(containerName, pool string) (string, error) {
				if pool == "" {
					pool = "default"
				}
				return "tank/" + pool + "/containers/" + containerName, nil
			},
		},
	}
}

// keyed makes the fake report a loaded key, so tests about the OTHER guards
// are not silently passing because the key guard fired first.
func keyed(z *zfsFake) *zfsFake {
	z.stdout["get"] = string(zfscrypt.KeyAvailable)
	return z
}

func stoppedBoxes() *stubBoxes {
	return &stubBoxes{state: pb.ContainerState_CONTAINER_STATE_STOPPED}
}

func TestRollbackContainerSnapshot_RollsBackAStoppedContainer(t *testing.T) {
	z := keyed(newZFSFake())
	s := rollbackFixture(t, z, stoppedBoxes())

	resp, err := s.RollbackContainerSnapshot(writeCtx("bob"),
		&pb.RollbackContainerSnapshotRequest{Username: "bob", Name: "nightly"})
	if err != nil {
		t.Fatalf("RollbackContainerSnapshot: %v", err)
	}
	if !z.ran("rollback tank/default/containers/bob-container@nightly") {
		t.Fatalf("zfs calls = %v, want a rollback of the container's dataset", z.calls)
	}
	if resp.GetContainerStopped() {
		t.Error("reported stopping a container that was already stopped")
	}
}

// Guard 1: a running container. ZFS would refuse the busy dataset anyway, but
// "dataset is busy" sends an operator looking for a mount problem instead of
// telling them the one thing they need to do.
func TestRollbackContainerSnapshot_RefusesARunningContainerUnlessForced(t *testing.T) {
	z := keyed(newZFSFake())
	boxes := &stubBoxes{state: pb.ContainerState_CONTAINER_STATE_RUNNING}
	s := rollbackFixture(t, z, boxes)

	_, err := s.RollbackContainerSnapshot(writeCtx("bob"),
		&pb.RollbackContainerSnapshotRequest{Username: "bob", Name: "nightly"})
	if err == nil {
		t.Fatal("rolled back a running container without --force")
	}
	if !strings.Contains(err.Error(), "running") || !strings.Contains(err.Error(), "force") {
		t.Errorf("the refusal does not say what is wrong or how to proceed: %v", err)
	}
	if z.ran("rollback") {
		t.Fatalf("the dataset was rolled back anyway — the guard returned an error AFTER "+
			"destroying data; calls=%v", z.calls)
	}
	if len(boxes.stopped) != 0 {
		t.Errorf("the container was stopped despite the refusal: %v", boxes.stopped)
	}
}

// With force, the daemon stops it first — and must stop it BEFORE rolling
// back, or ZFS refuses the busy dataset and force achieves nothing.
func TestRollbackContainerSnapshot_ForceStopsBeforeRollingBack(t *testing.T) {
	z := keyed(newZFSFake())
	rolledBackFirst := false
	boxes := &stubBoxes{state: pb.ContainerState_CONTAINER_STATE_RUNNING}
	boxes.stopSeen = func() { rolledBackFirst = z.ran("rollback") }
	s := rollbackFixture(t, z, boxes)

	resp, err := s.RollbackContainerSnapshot(writeCtx("bob"),
		&pb.RollbackContainerSnapshotRequest{Username: "bob", Name: "nightly", Force: true})
	if err != nil {
		t.Fatalf("forced rollback: %v", err)
	}
	if len(boxes.stopped) != 1 {
		t.Fatalf("the container was not stopped: %v", boxes.stopped)
	}
	if rolledBackFirst {
		t.Error("the rollback ran BEFORE the stop — ZFS refuses a busy dataset, so force would " +
			"fail on exactly the containers it exists for")
	}
	if !z.ran("rollback") {
		t.Fatalf("nothing was rolled back; calls=%v", z.calls)
	}
	// The caller has to know the container is down: its data just changed
	// underneath, and restarting is their decision, not the daemon's.
	if !resp.GetContainerStopped() {
		t.Error("the response does not report that the container was stopped and left stopped")
	}
}

// A container that cannot be inspected must not be rolled back on the
// assumption that it is stopped. "I could not tell" is not "it is safe".
func TestRollbackContainerSnapshot_RefusesWhenTheStateIsUnknown(t *testing.T) {
	z := keyed(newZFSFake())
	s := rollbackFixture(t, z, &stubBoxes{getErr: errors.New("incus unreachable")})

	if _, err := s.RollbackContainerSnapshot(writeCtx("bob"),
		&pb.RollbackContainerSnapshotRequest{Username: "bob", Name: "nightly"}); err == nil {
