package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// newStopForAutoSleepTestServer wires the minimum dependencies the
// thin wrapper needs: a backend the manager can call Stop / Get on,
// and an event emitter (StopContainer always emits a stopped event).
func newStopForAutoSleepTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) (*ContainerServer, *incustest.MockBackend) {
	t.Helper()
	mock := incustest.NewMockBackend()
	for n, info := range seed {
		mock.Containers[n] = info
	}
	return &ContainerServer{
		manager: container.NewWithBackend(mock),
		emitter: events.NewEmitter(events.NewBus()),
	}, mock
}

// TestStopForAutoSleep_CallsStopContainer — the wrapper drives the
// same plumbing as a manual stop: backend StopContainer is invoked
// with the "<username>-container" name and no force flag.
func TestStopForAutoSleep_CallsStopContainer(t *testing.T) {
	var stops int32
	var stoppedName string
	var stoppedForce bool
	s, mock := newStopForAutoSleepTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Running"},
	})
	mock.StopContainerFunc = func(name string, force bool) error {
		atomic.AddInt32(&stops, 1)
		stoppedName = name
		stoppedForce = force
		if c, ok := mock.Containers[name]; ok {
			c.State = "Stopped"
		}
		return nil
	}

	if err := s.StopForAutoSleep(testCtx(), "alice", "idle 90m", 90); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&stops); got != 1 {
		t.Fatalf("StopContainer calls = %d, want 1", got)
	}
	if stoppedName != "alice-container" {
		t.Errorf("stopped name = %q, want alice-container", stoppedName)
	}
	if stoppedForce {
		t.Errorf("auto-sleep stop must not use force=true (graceful stop only)")
	}
}

// TestStopForAutoSleep_PropagatesError — when the inner Stop fails
// and no peer pool is wired, the error bubbles up so the Manager can
// log it and skip the audit entry.
func TestStopForAutoSleep_PropagatesError(t *testing.T) {
	s, mock := newStopForAutoSleepTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Running"},
	})
	mock.StopContainerFunc = func(string, bool) error {
		return errors.New("incus refused stop")
	}
	err := s.StopForAutoSleep(testCtx(), "alice", "idle 90m", 90)
	if err == nil {
		t.Fatal("expected error to propagate from inner Stop")
	}
