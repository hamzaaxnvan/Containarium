package traffic

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// fakeContainerLister is a containerLister that never touches a real Incus
// server, so the incremental-refresh logic (#1541) is testable directly.
type fakeContainerLister struct {
	names       []string
	byName      map[string]*incus.ContainerInfo
	fetchCalls  []string // records every name GetContainerWithNetwork was asked for, in order
	namesErr    error
	fetchErrFor map[string]error
}

func (f *fakeContainerLister) GetInstanceNames() ([]string, error) {
	return f.names, f.namesErr
}

func (f *fakeContainerLister) GetContainerWithNetwork(name string) (*incus.ContainerInfo, error) {
	f.fetchCalls = append(f.fetchCalls, name)
	if err, ok := f.fetchErrFor[name]; ok {
		return nil, err
	}
	info, ok := f.byName[name]
	if !ok {
		return nil, errNotFound
	}
	return info, nil
}

func newCacheWithFake(f *fakeContainerLister) *ContainerCache {
	return &ContainerCache{
		incusClient: f,
		ipToName:    make(map[string]string),
		nameToIP:    make(map[string]string),
