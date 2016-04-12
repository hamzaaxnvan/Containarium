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
