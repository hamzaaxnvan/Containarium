package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CLI surface for container snapshots (#1160).
//
// The CLI is the canonical interface (CLAUDE.md: CLI-first, MCP wraps it), so
// these check what an operator actually sees — including the two numbers that
// are easy to confuse and the warning that stops a snapshot being mistaken
// for a backup.

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
// The commands print for humans, so the human-visible text is the contract
// worth asserting.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// captureErr runs fn with its output swallowed and returns only its error.
func captureErr(t *testing.T, fn func() error) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = fn() })
	return err
}

type fakeSnapshotAPI struct {
	created      *pb.CreateContainerSnapshotRequest
	deleted      *pb.DeleteContainerSnapshotRequest
	listResp     *pb.ListContainerSnapshotsResponse
	rolledBack   *pb.RollbackContainerSnapshotRequest
	rollbackResp *pb.RollbackContainerSnapshotResponse
	err          error
}

func (f *fakeSnapshotAPI) CreateContainerSnapshot(req *pb.CreateContainerSnapshotRequest) (*pb.CreateContainerSnapshotResponse, error) {
	f.created = req
	if f.err != nil {
		return nil, f.err
	}
	return &pb.CreateContainerSnapshotResponse{
		Snapshot: &pb.ContainerSnapshot{Name: req.GetName()},
	}, nil
}

func (f *fakeSnapshotAPI) ListContainerSnapshots(*pb.ListContainerSnapshotsRequest) (*pb.ListContainerSnapshotsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
