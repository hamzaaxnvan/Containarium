package tenantguard

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"

	"github.com/footprintai/containarium/internal/nicguard"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBackend is the smallest incus.Backend the reconciler needs, with
// every write counted: "writes nothing when converged" is the property
// most of these tests pin.
type fakeBackend struct {
	*incus.UnavailableBackend

	containers  []incus.ContainerInfo
	listErr     error
	firewall    string
	noBridgeACL bool // Incus 6.0.0: no network_bridge_acl_devices extension

	acls    map[string]api.NetworkACL
	devices map[string]map[string]map[string]string // container → device → keys

	created  []string
	updated  []string
	deleted  []string
	nicAdds  []string
	keyWrite []string
	aclErr   error

	fmu sync.Mutex // the fake itself is used from two goroutines in the serialisation test
	// ensureErrs fails EnsureNICDevice for the named container, and
	// onEnsureErr (if set) runs when it does: the tests use it to simulate a
	// rollback finishing, i.e. the instance disappearing from the host.
	ensureErrs  map[string]error
	onEnsureErr func(container string)
	afterList   func()
	// nicErrs are returned, in order, by successive SetDeviceConfig calls
	// before the real behaviour resumes (transient-conflict tests).
	nicErrs []error
	// active counts in-flight writes; maxActive records the peak, which
	// must stay 1 when the reconciler serialises correctly.
	active, maxActive int32
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
	f.fmu.Lock()
	defer f.fmu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := append([]incus.ContainerInfo(nil), f.containers...)
	if f.afterList != nil {
		// Runs after the snapshot is taken, so a test can change the host
		// between two successive lists.
		defer f.afterList()
	}
	return out, nil
}

func (f *fakeBackend) GetServerInfo() (*api.Server, error) {
	exts := []string{"network_acl", "network_bridge_acl", "network_bridge_acl_devices"}
	if f.noBridgeACL {
		exts = []string{"network_acl", "network_bridge_acl"} // Incus 6.0.0
	}
	return &api.Server{
		ServerUntrusted: api.ServerUntrusted{APIExtensions: exts},
		Environment:     api.ServerEnvironment{Firewall: f.firewall, ServerVersion: "7.4"},
	}, nil
}

func (f *fakeBackend) GetNetworkACL(name string) (*api.NetworkACL, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	acl, ok := f.acls[name]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := acl
	return &cp, nil
}

func toAPI(cfg incus.ACLConfig) api.NetworkACL {
	conv := func(rules []incus.ACLRule) []api.NetworkACLRule {
		var out []api.NetworkACLRule
		for _, r := range rules {
			out = append(out, api.NetworkACLRule{Action: r.Action, Source: r.Source, Destination: r.Destination,
				Protocol: r.Protocol, DestinationPort: r.DestinationPort, Description: r.Description, State: "enabled"})
		}
		return out
	}
	return api.NetworkACL{
		NetworkACLPost: api.NetworkACLPost{Name: cfg.Name},
		NetworkACLPut:  api.NetworkACLPut{Description: cfg.Description, Ingress: conv(cfg.IngressRules), Egress: conv(cfg.EgressRules)},
	}
}

func (f *fakeBackend) CreateNetworkACL(cfg incus.ACLConfig) error {
	f.fmu.Lock()
	defer f.fmu.Unlock()
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
	f.fmu.Lock()
	defer f.fmu.Unlock()
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

func (f *fakeBackend) ListNetworkACLs() ([]api.NetworkACL, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	out := make([]api.NetworkACL, 0, len(f.acls))
	for _, a := range f.acls {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeBackend) DeleteNetworkACL(name string) error {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	for _, devs := range f.devices {
		for _, dev := range devs {
			for _, n := range strings.Split(dev["security.acls"], ",") {
				if n == name {
					return errors.New("acl in use")
				}
			}
		}
	}
	delete(f.acls, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeBackend) EnsureNICDevice(container string, want incus.NICDevice) error {
	f.enter()
	defer f.leave()
	f.fmu.Lock()
	defer f.fmu.Unlock()
	if err := f.ensureErrs[container]; err != nil {
		if f.onEnsureErr != nil {
			f.onEnsureErr(container)
		}
		return err
	}
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

func (f *fakeBackend) enter() {
	n := atomic.AddInt32(&f.active, 1)
	for {
		m := atomic.LoadInt32(&f.maxActive)
		if n <= m || atomic.CompareAndSwapInt32(&f.maxActive, m, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond) // widen the window so an overlap is observable
}
func (f *fakeBackend) leave() { atomic.AddInt32(&f.active, -1) }

// AttachACLToContainer mirrors the real client (#2348): appends to an
// existing security.acls list, no write when already present.
func (f *fakeBackend) AttachACLToContainer(container, acl, device string) error {
	f.enter()
	defer f.leave()
	f.fmu.Lock()
	defer f.fmu.Unlock()
	dev, ok := f.devices[container][device]
