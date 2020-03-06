package incus

import (
	"errors"
	"testing"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// fakePoolServer answers only the per-pool calls. GetServerResources is not
// implemented: the embedded nil interface panics if StoragePoolUsages ever
// reaches for the heavy /1.0/resources read (#2325).
type fakePoolServer struct {
	incusclient.InstanceServer
	pools   []api.StoragePool
	listErr error
	res     map[string]*api.ResourcesStoragePool
}

func (f *fakePoolServer) GetStoragePools() ([]api.StoragePool, error) {
	return f.pools, f.listErr
}

func (f *fakePoolServer) GetStoragePoolResources(name string) (*api.ResourcesStoragePool, error) {
	if r, ok := f.res[name]; ok {
		return r, nil
	}
	return nil, errors.New("pool read failed")
}

func poolRes(used, total uint64) *api.ResourcesStoragePool {
	r := &api.ResourcesStoragePool{}
	r.Space.Used, r.Space.Total = used, total
	return r
}

func TestStoragePoolUsages_PerPoolAndSkipsFailedPool(t *testing.T) {
	srv := &fakePoolServer{
		pools: []api.StoragePool{{Name: "a", Driver: "zfs"}, {Name: "broken", Driver: "dir"}, {Name: "b", Driver: "dir"}},
		res:   map[string]*api.ResourcesStoragePool{"a": poolRes(96, 100), "b": poolRes(1, 1000)},
	}
	got, err := (&Client{server: srv}).StoragePoolUsages()
	if err != nil {
