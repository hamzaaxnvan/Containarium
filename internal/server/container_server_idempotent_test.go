package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/lxc/incus/v7/shared/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #3242: stop/start request an end state; when it already holds the call is a
// no-op success and none of the post-change work runs.

func newIdempotentServer(state string) (*ContainerServer, *incustest.MockBackend, *[]string) {
	mock := incustest.NewMockBackend()
	mock.Containers["alice-container"] = &incus.ContainerInfo{Name: "alice-container", State: state}
	var sets []string
	mock.SetConfigFunc = func(name, key, value string) error {
		sets = append(sets, key)
		return nil
	}
	s := &ContainerServer{manager: container.NewWithBackend(mock), emitter: events.NewEmitter(events.NewBus())}
	return s, mock, &sets
}

func TestStopContainer_AlreadyStoppedIsNoOpSuccess(t *testing.T) {
	s, mock, sets := newIdempotentServer("Stopped")
	stops := 0
	mock.StopContainerFunc = func(string, bool) error { stops++; return errors.New("The instance is already stopped") }

	resp, err := s.StopContainer(testCtx(), &pb.StopContainerRequest{Username: "alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Message, "already stopped") {
		t.Errorf("message %q does not name the already-stopped case", resp.Message)
	}
	if stops != 0 {
		t.Errorf("backend stop sent %d times, want 0", stops)
	}
	for _, k := range *sets {
		if k == incus.StoppedAtKey {
			t.Errorf("stopped_at re-stamped on an already-stopped instance")
		}
	}
}

