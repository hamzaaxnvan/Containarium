package server

import (
	"errors"
	"sync"
	"testing"

	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newAttributionTestServer wires a ContainerServer over a *MockBackend seeded
// with the given containers. AddLabel merges into an in-memory label store
// (prefix-stripped, like the real GetLabels) and GetLabels reads it back, so a
// test can assert the merged result the handler returns.
func newAttributionTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) (*ContainerServer, map[string]map[string]string) {
	t.Helper()
	mock := incustest.NewMockBackend()
	for name, info := range seed {
		mock.Containers[name] = info
	}
	var mu sync.Mutex
