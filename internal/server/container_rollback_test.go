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

