package incus

import (
	"errors"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// fakeListServer implements the instance-listing half of incus.InstanceServer
// and counts the requests each list costs. The embedded interface is nil: an
// unstubbed call panics instead of returning a zero value.
type fakeListServer struct {
	incusclient.InstanceServer

	instances []api.InstanceFull
	fullErr   error

	names, gets, states, fulls atomic.Int32
}

func (f *fakeListServer) GetInstanceNames(api.InstanceType) ([]string, error) {
	f.names.Add(1)
	out := make([]string, 0, len(f.instances))
	for _, i := range f.instances {
		out = append(out, i.Name)
	}
	return out, nil
}

func (f *fakeListServer) find(name string) (*api.InstanceFull, error) {
	for i := range f.instances {
		if f.instances[i].Name == name {
			return &f.instances[i], nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeListServer) GetInstance(name string) (*api.Instance, string, error) {
	f.gets.Add(1)
	i, err := f.find(name)
	if err != nil {
		return nil, "", err
	}
	inst := i.Instance
	return &inst, "", nil
}

func (f *fakeListServer) GetInstanceState(name string) (*api.InstanceState, string, error) {
	f.states.Add(1)
	i, err := f.find(name)
	if err != nil {
		return nil, "", err
	}
	return i.State, "", nil
}

func (f *fakeListServer) GetInstancesFull(api.InstanceType) ([]api.InstanceFull, error) {
	f.fulls.Add(1)
	if f.fullErr != nil {
		return nil, f.fullErr
	}
	return f.instances, nil
}

func listFixture() []api.InstanceFull {
	created := time.Unix(1_700_000_000, 0).UTC()
	return []api.InstanceFull{
		{
			Instance: api.Instance{
				Name: "a-box", Status: "Running", Type: "container", CreatedAt: created,
				InstancePut: api.InstancePut{
					Config: map[string]string{
						TenantLabelKey: "a-tenant", "limits.memory": "2GB", "limits.cpu": "2",
						"user.containarium.label.team": "x",
					},
					Devices: map[string]map[string]string{"root": {"type": "disk", "size": "10GB"}},
				},
			},
			State: &api.InstanceState{
				Pid: 4242,
				Network: map[string]api.InstanceStateNetwork{
					"eth0": {Addresses: []api.InstanceStateNetworkAddress{{Family: "inet", Address: "192.0.2.10", Scope: "global"}}},
