package server

import (
	"errors"
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

// dpMockCall captures a single Set/UnsetConfig invocation the handler makes,
// mirroring ttlMockCall in container_server_ttl_test.go.
type dpMockCall struct {
	kind  string // "set" or "unset"
	name  string
	key   string
	value string
}

// newDeletePolicyTestServer wires a ContainerServer over a *MockBackend seeded
// with the given containers, persisting delete-policy writes into the mock so a
// follow-up Get reflects them. Returns the captured-call slice for inspection.
func newDeletePolicyTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) (*ContainerServer, *[]dpMockCall) {
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
		if c, ok := mock.Containers[name]; ok && key == incus.DeletePolicyKey {
			c.DeletePolicy = value
		}
		return nil
	}
	mock.UnsetConfigFunc = func(name, key string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, dpMockCall{kind: "unset", name: name, key: key})
		if c, ok := mock.Containers[name]; ok && key == incus.DeletePolicyKey {
			c.DeletePolicy = ""
		}
		return nil
	}
	mgr := container.NewWithBackend(mock)
	return &ContainerServer{manager: mgr, boxBackend: boxlxc.New(mgr)}, &calls
}

// TestSetContainerDeletePolicy_ProtectStampsKey — PROTECTED writes one SetConfig
// with the canonical "protected" value, and the response echoes the policy.
func TestSetContainerDeletePolicy_ProtectStampsKey(t *testing.T) {
	s, calls := newDeletePolicyTestServer(t, map[string]*incus.ContainerInfo{
		"alice-container": {Name: "alice-container", State: "Running"},
