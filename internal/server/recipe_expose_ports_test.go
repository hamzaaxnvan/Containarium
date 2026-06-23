package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/network"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #1462: RecipePort can now describe a raw TCP/UDP passthrough instead of an
// HTTP subdomain. These tests exercise exposePorts' branching, the
// fail-loud-on-conflict behavior, and the teardown cascade — all against
// fakes, no real Incus/Postgres needed.

// fakeGetOnlyIncusBackend answers GetContainer with a fixed ContainerInfo and
// panics on every other incus.Backend method — exposePorts only ever calls
// Manager.Get, which is the only method this needs to stand in for.
type fakeGetOnlyIncusBackend struct {
	incus.Backend
	info *incus.ContainerInfo
}

func (f *fakeGetOnlyIncusBackend) GetContainer(name string) (*incus.ContainerInfo, error) {
	return f.info, nil
}

// fakePassthroughStore is an in-memory network.PassthroughStore for tests
// that don't want a real Postgres round trip.
type fakePassthroughStore struct {
	byKey map[string]*network.PassthroughRecord // "port/protocol" -> record
}

func newFakePassthroughStore() *fakePassthroughStore {
	return &fakePassthroughStore{byKey: map[string]*network.PassthroughRecord{}}
}

func passthroughKey(port int, protocol string) string {
	return fmt.Sprintf("%s/%d", protocol, port)
}

func (f *fakePassthroughStore) Save(_ context.Context, route *network.PassthroughRecord) error {
	cp := *route
	f.byKey[passthroughKey(route.ExternalPort, route.Protocol)] = &cp
	return nil
}
