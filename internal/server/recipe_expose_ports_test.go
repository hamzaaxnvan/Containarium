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

func (f *fakePassthroughStore) GetByPortProtocol(_ context.Context, externalPort int, protocol string) (*network.PassthroughRecord, error) {
	r, ok := f.byKey[passthroughKey(externalPort, protocol)]
	if !ok {
		return nil, network.ErrPassthroughNotFound
	}
	return r, nil
}

func (f *fakePassthroughStore) List(_ context.Context, _ bool) ([]*network.PassthroughRecord, error) {
	out := make([]*network.PassthroughRecord, 0, len(f.byKey))
	for _, r := range f.byKey {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakePassthroughStore) ListByContainer(_ context.Context, containerName string) ([]*network.PassthroughRecord, error) {
	var out []*network.PassthroughRecord
	for _, r := range f.byKey {
		if r.ContainerName == containerName {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakePassthroughStore) Delete(_ context.Context, externalPort int, protocol string) error {
	delete(f.byKey, passthroughKey(externalPort, protocol))
	return nil
}

func (f *fakePassthroughStore) SetActive(_ context.Context, externalPort int, protocol string, active bool) error {
	if r, ok := f.byKey[passthroughKey(externalPort, protocol)]; ok {
		r.Active = active
	}
	return nil
}

func (f *fakePassthroughStore) Count(_ context.Context, _ bool) (int32, error) {
	return safecast.I32(len(f.byKey)), nil
}

var _ network.PassthroughStore = (*fakePassthroughStore)(nil)

// adminCtx (defined in rbac_phase_1_4_tenant_test.go) is reused here: it's
// the context shape a real recipe-deploy caller must already carry to get
// ANY port exposed — AddRoute has required auth.RoleAdmin since before this
// change, so the TCP/UDP passthrough path requiring it too is the existing
// precedent, not a new bar.

func newExposePortsTestServer(ip string) (*RecipeServer, *fakePassthroughStore) {
	manager := container.NewWithBackend(&fakeGetOnlyIncusBackend{
		info: &incus.ContainerInfo{Name: "alice-container", IPAddress: ip},
	})
	store := newFakePassthroughStore()
	return &RecipeServer{
		containers: &ContainerServer{manager: manager},
		network:    &NetworkServer{baseDomain: "example.com", passthroughStore: store},
	}, store
}

func TestExposePorts_TCPPortRegistersPassthroughNotHTTPRoute(t *testing.T) {
	srv, store := newExposePortsTestServer("10.0.3.5")
	recipe := &pb.Recipe{
		Id: "risingwave",
		Ports: []*pb.RecipePort{
			{ContainerPort: 4566, Protocol: pb.RouteProtocol_ROUTE_PROTOCOL_TCP},
		},
	}

	url, endpoints, warnings := srv.exposePorts(adminCtx(), recipe, "alice")
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if url != "" {
		t.Errorf("url = %q, want empty — a TCP port must not produce an HTTPS URL", url)
	}
	if len(endpoints) != 1 || !strings.Contains(endpoints[0], "4566") {
		t.Fatalf("endpoints = %v, want one entry naming port 4566", endpoints)
	}

	rec, err := store.GetByPortProtocol(context.Background(), 4566, "tcp")
	if err != nil {
		t.Fatalf("GetByPortProtocol: %v", err)
	}
	if rec.TargetIP != "10.0.3.5" || rec.TargetPort != 4566 {
		t.Errorf("passthrough record = %+v, want target 10.0.3.5:4566", rec)
	}
}

func TestExposePorts_ExternalPortDefaultsToContainerPort(t *testing.T) {
	srv, store := newExposePortsTestServer("10.0.3.5")
	recipe := &pb.Recipe{Ports: []*pb.RecipePort{
		{ContainerPort: 6379, Protocol: pb.RouteProtocol_ROUTE_PROTOCOL_TCP},
	}}

	if _, _, warnings := srv.exposePorts(adminCtx(), recipe, "alice"); len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if _, err := store.GetByPortProtocol(context.Background(), 6379, "tcp"); err != nil {
		t.Fatalf("expected the passthrough at the container_port (6379) when external_port is unset: %v", err)
	}
