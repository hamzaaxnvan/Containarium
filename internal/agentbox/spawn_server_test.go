//go:build !windows

package agentbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// These exercise SpawnServer directly (no socket) for the RPC-logic cases,
// and go through a real StartSpawnListener + a real dialed gRPC client for
// the contract/transport cases — no live Incus host or container needed
// for any of it: a resident unix-socket gRPC server with real fork/exec is
// fully testable in a plain Linux dev environment (#1488 Phase 2's own
// scoping note).

func TestSpawnServer_Spawn_ReturnsPidAndReaps(t *testing.T) {
	t.Cleanup(setProcessLogDirForTest(t.TempDir()))
	t.Cleanup(func() { killAllAndReset(t) })
	s := NewSpawnServer()

	resp, err := s.Spawn(context.Background(), &pb.SpawnRequest{
		Command: "sleep 0.05",
		Name:    "reap-test",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if resp.Pid <= 0 {
		t.Fatalf("pid = %d, want > 0", resp.Pid)
	}
	if resp.Name != "reap-test" || resp.LogPath == "" {
		t.Errorf("resp = %+v", resp)
	}

	// isAlive(pid) via kill(pid, 0) stays true for a zombie — a still-valid
	// PID slot the kernel hasn't freed yet — so it flipping to false is
	// specifically evidence cmd.Wait() completed and reaped the child, not
