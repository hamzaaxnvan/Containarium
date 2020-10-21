package cluster

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lxc/incus/v7/shared/api"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// ErrVMsUnsupported is the typed create-time refusal for hosts that
// cannot run Incus VMs (the design's hard requirement — no silent
// degrade to shared-kernel containers).
var ErrVMsUnsupported = errors.New("this backend cannot run virtual machines (KVM/qemu unavailable); managed clusters require VM capability")

// IncusHost adapts pkg/core/incus.Client to the VMHost seam. Thin by
// design: every method is one client call plus shaping; the sequences
// that matter are in Manager and tested against a fake.
type IncusHost struct {
	client *incus.Client
}

// NewIncusHost wraps an incus client.
func NewIncusHost(c *incus.Client) *IncusHost { return &IncusHost{client: c} }

func (h *IncusHost) VMCapable() error {
	info, err := h.client.GetServerInfo()
	if err != nil {
		return fmt.Errorf("query incus server: %w", err)
	}
	// Incus reports VM support by listing "qemu" among its drivers.
	if !strings.Contains(info.Environment.Driver, "qemu") {
		return ErrVMsUnsupported
	}
	return nil
}

// ContainerNodeCapable probes the container-node preconditions on the
// real host (#1429): the observation lives in GatherContainerNodeFacts
// and the verdict in CheckContainerNodeFacts, both in
// containerprofile.go — the one place holding this knowledge.
func (h *IncusHost) ContainerNodeCapable() error {
	return CheckContainerNodeFacts(GatherContainerNodeFacts("/", func() (string, error) {
		info, err := h.client.GetServerInfo()
		if err != nil {
			return "", err
		}
		return info.Environment.Driver, nil
	}))
}

// NodeCapacityCapable is the per-node half of the container probe
// (#1439): the kernel preconditions can all hold on a host that still
// cannot honour the size this node asked for. Refusing here beats
// creating a node that would advertise capacity it does not have —
// the scheduler and the autoscaler both believe node allocatable.
func (h *IncusHost) NodeCapacityCapable(spec NodeSpec) error {
	cpu, mem, err := HostCapacity("/")
	if err != nil {
		// Fail closed: an unreadable /proc means the lie cannot be
		// measured, not that there is no lie.
		return fmt.Errorf("%w: cannot read host capacity, so a container node's advertised size cannot be made truthful: %v",
			ErrContainerNodesUnsupported, err)
	}
	return CheckNodeCapacity(cpu, mem, spec)
}

// nodeContainerConfig is the Incus config one cluster node is created
// from. Pure (no Incus calls) so the shape Incus validates is
// unit-testable — see nodeconfig_test.go and #1435.
func nodeContainerConfig(spec NodeSpec, isolation Isolation, pool string) incus.ContainerConfig {
	cfg := incus.ContainerConfig{
		Name:         spec.Name,
		Image:        "images:ubuntu/24.04", // the nodevm default; VM image bake is a later phase
		CPU:          spec.CPU,
		Memory:       spec.Memory,
		InstanceType: api.InstanceTypeVM,
		AutoStart:    true,
	}
	if isolation == IsolationContainer {
		// The container node profile — nesting, no privileged — comes
		// from ContainerNodeConfig; anything that is not the explicit
		// weaker class provisions a VM (never default to the weaker
		// boundary).
		cfg.InstanceType = api.InstanceTypeContainer
		cfg.ExtraConfig = ContainerNodeConfig()
	}
	if spec.Disk != "" {
		// Path and Pool alongside the size, as every other caller in the
