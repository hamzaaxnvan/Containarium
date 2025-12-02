package coreguard

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/lxc/incus/v7/shared/api"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// fakeBackend is the smallest incus.Backend the reconciler needs: a
// container list, a server info, an ACL store and per-container NIC
// devices — with every write counted, because "writes nothing when
// converged" is the property most of these tests pin.
type fakeBackend struct {
	*incus.UnavailableBackend

	containers []incus.ContainerInfo
	listErr    error
	firewall   string

	acls    map[string]api.NetworkACL
	devices map[string]map[string]map[string]string // container → device → keys

	created  []string // ACL names
	updated  []string // ACL names
	nicAdds  []string // "container/device"
	keyWrite []string // "container/device"
	aclErr   error    // injected on Create/Update
}

func newFakeBackend(firewall string, containers ...incus.ContainerInfo) *fakeBackend {
	return &fakeBackend{
		UnavailableBackend: incus.NewUnavailableBackend(),
		containers:         containers,
		firewall:           firewall,
		acls:               map[string]api.NetworkACL{},
		devices:            map[string]map[string]map[string]string{},
	}
}

func (f *fakeBackend) ListContainers() ([]incus.ContainerInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]incus.ContainerInfo, len(f.containers))
	copy(out, f.containers)
	return out, nil
}

func (f *fakeBackend) GetServerInfo() (*api.Server, error) {
	return &api.Server{
		ServerUntrusted: api.ServerUntrusted{APIExtensions: []string{"network_acl", "network_bridge_acl", "network_bridge_acl_devices"}},
		Environment:     api.ServerEnvironment{Firewall: f.firewall},
	}, nil
}

func (f *fakeBackend) GetNetworkACL(name string) (*api.NetworkACL, error) {
	acl, ok := f.acls[name]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := acl
	return &cp, nil
}

func toAPI(cfg incus.ACLConfig) api.NetworkACL {
	var in []api.NetworkACLRule
	for _, r := range cfg.IngressRules {
		in = append(in, api.NetworkACLRule{Action: r.Action, Source: r.Source, Destination: r.Destination,
			Protocol: r.Protocol, DestinationPort: r.DestinationPort, Description: r.Description, State: "enabled"})
	}
	return api.NetworkACL{
		NetworkACLPost: api.NetworkACLPost{Name: cfg.Name},
		NetworkACLPut:  api.NetworkACLPut{Description: cfg.Description, Ingress: in},
	}
}

func (f *fakeBackend) CreateNetworkACL(cfg incus.ACLConfig) error {
	if f.aclErr != nil {
		return f.aclErr
	}
	if _, exists := f.acls[cfg.Name]; exists {
		return errors.New("already exists")
	}
	f.acls[cfg.Name] = toAPI(cfg)
	f.created = append(f.created, cfg.Name)
	return nil
}

func (f *fakeBackend) UpdateNetworkACL(name string, cfg incus.ACLConfig) error {
	if f.aclErr != nil {
		return f.aclErr
	}
	if _, exists := f.acls[name]; !exists {
		return errors.New("not found")
	}
	f.acls[name] = toAPI(cfg)
	f.updated = append(f.updated, name)
	return nil
}

// EnsureNICDevice mirrors the real client: a write only when the device
// is not already instance-local.
func (f *fakeBackend) EnsureNICDevice(container string, want incus.NICDevice) error {
	if f.devices[container] == nil {
		f.devices[container] = map[string]map[string]string{}
	}
	if _, ok := f.devices[container][want.Name]; ok {
		return nil
	}
	f.devices[container][want.Name] = map[string]string{"type": "nic", "name": want.Name, "network": want.Network}
	f.nicAdds = append(f.nicAdds, container+"/"+want.Name)
	return nil
}

// SetDeviceConfig mirrors the real client: refuses a device that is not
// instance-local, writes only when a key differs.
func (f *fakeBackend) SetDeviceConfig(container, device string, keys map[string]string) error {
	dev, ok := f.devices[container][device]
	if !ok {
		return errors.New("device " + device + " not instance-local on " + container)
	}
	changed := false
	for k, v := range keys {
		if dev[k] != v {
			dev[k] = v
			changed = true
		}
	}
	if changed {
		f.keyWrite = append(f.keyWrite, container+"/"+device)
	}
	return nil
}

func (f *fakeBackend) writes() int {
	return len(f.created) + len(f.updated) + len(f.nicAdds) + len(f.keyWrite)
}

func core(name string, role incus.Role, ip string) incus.ContainerInfo {
	return incus.ContainerInfo{Name: name, Role: role, IPAddress: ip, State: "Running"}
}

func tenant(name, ip string) incus.ContainerInfo {
	return incus.ContainerInfo{Name: name, IPAddress: ip, State: "Running"}
}

func fullHost() *fakeBackend {
	return newFakeBackend("nftables",
		core("containarium-core-postgres", incus.RolePostgres, "10.100.0.242"),
		core("containarium-core-victoriametrics", incus.RoleVictoriaMetrics, "10.100.0.243"),
		core("containarium-core-caddy", incus.RoleCaddy, "10.100.0.241"),
		core("cloud-control-plane", incus.RoleControlPlane, "10.100.0.200"),
		tenant("alice-container", "10.100.0.17"),
	)
}

func enforceCfg() Config {
	return Config{Mode: ModeEnforce, Bridge: "incusbr0", BridgeCIDR: "10.100.0.1/24", NICDevice: "eth0"}
}

func TestReconcile_CreatesMissingACLsAndAttaches(t *testing.T) {
	f := fullHost()
	r := NewReconciler(f, enforceCfg())

	if err := r.ReconcileOnce(context.Background()); err != nil {
