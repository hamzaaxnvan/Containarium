package incus

import (
	"errors"
	"strings"
	"testing"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// fakeDeviceServer serves one instance and records every UpdateInstance
// so a test can assert on exactly what was written (or that nothing was).
type fakeDeviceServer struct {
	incusclient.InstanceServer

	inst   *api.Instance
	getErr error

	storagePool      *api.StoragePool
	storagePoolErr   error
	storageVolume    *api.StorageVolume
	storageVolumeErr error
	updateErr        error
	afterUpdate      *api.Instance
	afterUpdateHook  func()

	updates []api.InstancePut
	events  []string
}

func (f *fakeDeviceServer) GetInstance(string) (*api.Instance, string, error) {
	if f.getErr != nil {
		return nil, "", f.getErr
	}
	// Hand out a copy so the code under test can't mutate the fixture
	// behind the test's back.
	cp := *f.inst
	cp.Devices = cloneDevices(f.inst.Devices)
	cp.ExpandedDevices = cloneDevices(f.inst.ExpandedDevices)
	return &cp, "etag", nil
}

func (f *fakeDeviceServer) UpdateInstance(_ string, put api.InstancePut, _ string) (incusclient.Operation, error) {
	f.updates = append(f.updates, put)
	f.events = append(f.events, "update")
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.afterUpdate != nil {
		f.inst = f.afterUpdate
	}
	if f.afterUpdateHook != nil {
		f.afterUpdateHook()
	}
	return fakeOp{}, nil
}

func (f *fakeDeviceServer) GetStoragePool(string) (*api.StoragePool, string, error) {
	if f.storagePoolErr != nil {
		return nil, "", f.storagePoolErr
	}
	return f.storagePool, "etag", nil
}

func (f *fakeDeviceServer) GetStoragePoolVolume(string, string, string) (*api.StorageVolume, string, error) {
	if f.storageVolumeErr != nil {
		return nil, "", f.storageVolumeErr
	}
	return f.storageVolume, "etag", nil
}

type fakeOp struct{ incusclient.Operation }

func (fakeOp) Wait() error { return nil }

func cloneDevices(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for name, dev := range in {
		d := make(map[string]string, len(dev))
		for k, v := range dev {
			d[k] = v
		}
		out[name] = d
	}
	return out
}

// profileNIC is what a container that inherits eth0 from the default
// profile looks like: nothing instance-local, but the NIC shows up in the
// expanded view.
func profileNIC() *api.Instance {
	return &api.Instance{
		InstancePut: api.InstancePut{Devices: map[string]map[string]string{}},
		ExpandedDevices: map[string]map[string]string{
			"eth0": {"type": "nic", "name": "eth0", "network": "incusbr0"},
		},
	}
}

func withRootDisk(inst *api.Instance, driver string) *fakeDeviceServer {
	inst.ExpandedDevices["root"] = map[string]string{
		"type": "disk",
		"path": "/",
		"pool": "default",
		"size": "7GiB",
	}
	return &fakeDeviceServer{inst: inst, storagePool: &api.StoragePool{Name: "default", Driver: driver}}
}

func TestEnsureNICDevice(t *testing.T) {
	tests := []struct {
		name       string
		inst       *api.Instance
		want       NICDevice
		wantWrites int
		wantDevice map[string]string // the eth0 written, when wantWrites == 1
	}{
		{
			name:       "profile-inherited NIC gets an instance-local override",
			inst:       profileNIC(),
			want:       NICDevice{Name: "eth0", Network: "incusbr0", IPv4Address: "10.100.0.242"},
			wantWrites: 1,
			wantDevice: map[string]string{"type": "nic", "name": "eth0", "network": "incusbr0", "ipv4.address": "10.100.0.242"},
		},
		{
			name:       "empty IPv4 does not write an ipv4.address key",
			inst:       profileNIC(),
			want:       NICDevice{Name: "eth0", Network: "incusbr0"},
			wantWrites: 1,
			wantDevice: map[string]string{"type": "nic", "name": "eth0", "network": "incusbr0"},
		},
		{
			name: "instance-local and equal is a no-op",
			inst: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{
				"eth0": {"type": "nic", "name": "eth0", "network": "incusbr0", "ipv4.address": "10.100.0.242"},
			}}},
			want:       NICDevice{Name: "eth0", Network: "incusbr0", IPv4Address: "10.100.0.242"},
			wantWrites: 0,
		},
		{
			name: "instance-local with a different address is updated and keeps unrelated keys",
			inst: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{
				"eth0": {"type": "nic", "name": "eth0", "network": "incusbr0", "ipv4.address": "10.100.0.9", "security.acls": "keep-me"},
			}}},
			want:       NICDevice{Name: "eth0", Network: "incusbr0", IPv4Address: "10.100.0.242"},
			wantWrites: 1,
			wantDevice: map[string]string{"type": "nic", "name": "eth0", "network": "incusbr0", "ipv4.address": "10.100.0.242", "security.acls": "keep-me"},
		},
		{
			name: "instance-local with an address is left alone when the caller asks for DHCP",
			inst: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{
				"eth0": {"type": "nic", "name": "eth0", "network": "incusbr0", "ipv4.address": "10.100.0.9"},
			}}},
			want:       NICDevice{Name: "eth0", Network: "incusbr0"},
			wantWrites: 0,
		},
		{
			name:       "no NIC anywhere creates one from scratch",
			inst:       &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{}}},
			want:       NICDevice{Name: "eth0", Network: "incusbr0"},
			wantWrites: 1,
			wantDevice: map[string]string{"type": "nic", "name": "eth0", "network": "incusbr0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDeviceServer{inst: tc.inst}
			c := &Client{server: f}

			if err := c.EnsureNICDevice("box", tc.want); err != nil {
				t.Fatalf("EnsureNICDevice: %v", err)
			}
			if got := len(f.updates); got != tc.wantWrites {
				t.Fatalf("UpdateInstance calls = %d, want %d", got, tc.wantWrites)
			}
			if tc.wantWrites == 0 {
				return
			}
			got := f.updates[0].Devices[tc.want.Name]
			if len(got) != len(tc.wantDevice) {
				t.Fatalf("device written = %v, want %v", got, tc.wantDevice)
			}
			for k, v := range tc.wantDevice {
				if got[k] != v {
					t.Errorf("device[%q] = %q, want %q (full: %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestEnsureNICDevice_GetErrorWritesNothing(t *testing.T) {
	boom := errors.New("incus down")
	f := &fakeDeviceServer{getErr: boom}
	c := &Client{server: f}

	err := c.EnsureNICDevice("box", NICDevice{Name: "eth0", Network: "incusbr0"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapping %v", err, boom)
	}
	if len(f.updates) != 0 {
		t.Fatalf("UpdateInstance called %d times on a failed read", len(f.updates))
	}
}

func TestEnsureNICDevice_RejectsEmptyName(t *testing.T) {
	f := &fakeDeviceServer{inst: profileNIC()}
	c := &Client{server: f}
	if err := c.EnsureNICDevice("box", NICDevice{Network: "incusbr0"}); err == nil {
		t.Fatal("expected an error for an empty device name")
	}
	if len(f.updates) != 0 {
		t.Fatal("must not write when the request is invalid")
	}
}

func TestEnsureNICDevice_PreparesZFSQuotaHeadroomBeforeUpdate(t *testing.T) {
	f := withRootDisk(profileNIC(), "zfs")
	properties := map[string]string{"used": "7516192768", "quota": "7516192768"}
	c := &Client{
		server: f,
		zfsGetPropertyFn: func(_ string, property string) (string, error) {
			return properties[property], nil
		},
		zfsSetPropertyFn: func(_ string, property, value string) error {
			f.events = append(f.events, "set:"+property+":"+value)
			properties[property] = value
			return nil
		},
	}

	if err := c.EnsureNICDevice("box", NICDevice{Name: "eth0", Network: "incusbr0"}); err != nil {
		t.Fatalf("EnsureNICDevice: %v", err)
