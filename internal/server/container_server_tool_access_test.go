package server

import (
	"sync"
	"testing"

	"github.com/footprintai/containarium/pkg/core/box"
	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newToolAccessTestServer wires a ContainerServer over a MockBackend seeded with
// the given containers. Writes to the tool-access key land in the mock, so a
// follow-up Get reflects them, and every Set/UnsetConfig is captured.
func newToolAccessTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) (*ContainerServer, *incustest.MockBackend, *[]dpMockCall) {
	t.Helper()
	mock := incustest.NewMockBackend()
	for name, info := range seed {
		mock.Containers[name] = info
	}
	var mu sync.Mutex
	calls := make([]dpMockCall, 0, 2)
	mock.SetConfigFunc = func(name, key, value string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, dpMockCall{kind: "set", name: name, key: key, value: value})
		if c, ok := mock.Containers[name]; ok && key == incus.ToolAccessKey {
			c.ToolAccess = value
		}
		return nil
	}
	mock.UnsetConfigFunc = func(name, key string) error {
		mu.Lock()
