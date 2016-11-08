package server

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// setConfigCall captures the args of one mocked SetConfig invocation.
type setConfigCall struct {
	name  string
	key   string
	value string
}

// newAutoSleepTestServer builds a ContainerServer wired to a mock
// incus.Backend + a real Manager, returning a slice the caller can
// inspect after handler invocation. seed populates the mock with
// initial container state.
func newAutoSleepTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) (*ContainerServer, *[]setConfigCall, *incustest.MockBackend) {
	t.Helper()
	mock := incustest.NewMockBackend()
	for name, info := range seed {
		mock.Containers[name] = info
	}
	var mu sync.Mutex
	calls := make([]setConfigCall, 0, 4)
	mock.SetConfigFunc = func(name, key, value string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, setConfigCall{name: name, key: key, value: value})
		// Also persist into the mock state so subsequent Get() reflects
		// the change — mirrors real Incus semantics.
		if c, ok := mock.Containers[name]; ok {
			if key == incus.AutoSleepEnabledKey {
				c.AutoSleepEnabled = value == "true"
			}
			if key == incus.IdleThresholdMinutesKey {
				if n, err := strconv.Atoi(value); err == nil {
					c.IdleThresholdMinutes = safecast.I32(n)
				}
			}
		}
		return nil
