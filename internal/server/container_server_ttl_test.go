package server

import (
	"errors"
	"sync"
	"testing"
	"time"

	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ttlMockCall is a single observed Set/UnsetConfig invocation. We
// capture both flavours into the same channel so the assertions can
// read "what calls did the handler make, in order" without juggling
// two slices.
type ttlMockCall struct {
	kind  string // "set" or "unset"
	name  string
	key   string
	value string
}

// newTTLTestServer wires a ContainerServer over a *MockBackend seeded
// with the given containers, and returns the captured-call slice the
// caller can inspect after a handler invocation. Mirrors the shape of
// newAutoSleepTestServer from toggle_auto_sleep_test.go so the two
// container-handler test files stay readable side-by-side.
func newTTLTestServer(t *testing.T, seed map[string]*incus.ContainerInfo) (*ContainerServer, *[]ttlMockCall, *incustest.MockBackend) {
	t.Helper()
	mock := incustest.NewMockBackend()
	for name, info := range seed {
		mock.Containers[name] = info
	}
	var mu sync.Mutex
	calls := make([]ttlMockCall, 0, 4)
	mock.SetConfigFunc = func(name, key, value string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, ttlMockCall{kind: "set", name: name, key: key, value: value})
		// Persist into the mock so subsequent Get reflects the change.
		if c, ok := mock.Containers[name]; ok && key == incus.TTLExpiresAtKey {
			if t2, err := time.Parse(time.RFC3339, value); err == nil {
				c.TTLExpiresAt = t2
			}
		}
		return nil
	}
	mock.UnsetConfigFunc = func(name, key string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, ttlMockCall{kind: "unset", name: name, key: key})
		if c, ok := mock.Containers[name]; ok && key == incus.TTLExpiresAtKey {
			c.TTLExpiresAt = time.Time{}
		}
		return nil
	}
	mgr := container.NewWithBackend(mock)
	return &ContainerServer{manager: mgr, boxBackend: boxlxc.New(mgr)}, &calls, mock
}
