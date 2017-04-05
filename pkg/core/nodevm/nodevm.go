// Package nodevm provisions Incus VMs that each become a Containarium
// backend ("node") — carving one physical host into multiple pool-tagged
// nodes (e.g. a GPU node + a CPU node). See docs/NODE-VM-PROVISIONING.md.
//
// This is day-0 host bootstrap, not a daemon RPC: it runs on the
// hypervisor host, drives the `incus` CLI to create VMs, and bootstraps a
// daemon+tunnel inside each (the same sequence scripts/setup-peer.sh runs,
// just executed in the guest). The CLI command group `containarium node`
// is a thin wrapper over this package.
//
// The incus-argv builders and the in-guest bootstrap renderer are pure
// functions and are unit-tested; the actual exec needs a host with Incus +
// the qemu driver and is not exercised in CI.
package nodevm

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is the node flavor.
type Kind string

const (
	KindCPU Kind = "cpu"
	KindGPU Kind = "gpu"
)

// DefaultImage is the base image for a node VM.
const DefaultImage = "images:ubuntu/24.04"

// gpuDeviceName is the Incus device name used for the passed-through GPU.
const gpuDeviceName = "gpu0"

// Runner executes `incus <args...>` (and a couple of guest helpers) and
// returns combined output. Injected so tests can fake it.
type Runner interface {
	Run(args ...string) (string, error)
}

// Spec describes one node VM to provision.
type Spec struct {
	Name     string // VM / node name, e.g. "gpu-node"
	Kind     Kind   // cpu | gpu
	Pool     string // Containarium pool tag, e.g. "gpu" / "cpu"
	CPU      int    // vCPUs
	Memory   string // e.g. "64GiB"
	Disk     string // e.g. "200GiB" (root disk); empty = image default
	Image    string // base image; empty = DefaultImage
	GPUPCI   string // PCI address for KindGPU, e.g. "0000:01:00.0"
	Sentinel string // sentinel addr host:port the in-guest tunnel dials
	// TunnelToken is the credential the in-guest tunnel uses. It is NOT
	// placed on any argv — Provision delivers it as a perm-tight file
	// inside the VM. Kept here so callers pass it through one struct.
	TunnelToken string
}

// Validate checks a Spec before any host mutation.
func (s Spec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch s.Kind {
	case KindCPU, KindGPU:
	default:
		return fmt.Errorf("kind must be %q or %q, got %q", KindCPU, KindGPU, s.Kind)
	}
	if s.Kind == KindGPU && s.GPUPCI == "" {
		return fmt.Errorf("kind %q requires --gpu pci=<addr>", KindGPU)
	}
	if s.CPU <= 0 {
		return fmt.Errorf("cpu must be > 0")
	}
	if s.Memory == "" {
		return fmt.Errorf("memory is required (e.g. 64GiB)")
	}
	if s.Sentinel == "" {
		return fmt.Errorf("sentinel address is required")
	}
	if s.Pool == "" {
		return fmt.Errorf("pool is required")
	}
	return nil
}

func (s Spec) image() string {
	if s.Image == "" {
		return DefaultImage
	}
	return s.Image
}

// SpotID is the tunnel/peer identity this node registers under — derived
// from name+pool so it's stable and human-readable in `backends list`.
func (s Spec) SpotID() string {
	return s.Name + "-" + s.Pool
}

// launchArgs builds `incus launch <image> <name> --vm -c limits.cpu=N
// -c limits.memory=X [--device root,size=<disk>]`.
func launchArgs(s Spec) []string {
	args := []string{
		"launch", s.image(), s.Name, "--vm",
		"-c", "limits.cpu=" + strconv.Itoa(s.CPU),
		"-c", "limits.memory=" + s.Memory,
	}
	if s.Disk != "" {
		args = append(args, "--device", "root,size="+s.Disk)
	}
	return args
}

// gpuDeviceArgs builds `incus config device add <name> gpu0 gpu pci=<addr>`.
func gpuDeviceArgs(name, pci string) []string {
	return []string{"config", "device", "add", name, gpuDeviceName, "gpu", "pci=" + pci}
}

func destroyArgs(name string) []string { return []string{"delete", "-f", name} }

// listArgs lists only VMs. Note: `--vm` is a `launch`/`init` flag, NOT a
// `list` flag — VM filtering on `list` uses the positional `type=` filter.
func listArgs() []string {
	return []string{"list", "type=virtual-machine", "--format", "csv", "-c", "ns"}
}

// RenderBootstrap produces the in-guest bootstrap script — the
// setup-peer.sh-equivalent run inside the VM on first provision. It does
// NOT contain the tunnel token (delivered separately as a file at
// tokenPath); the script reads it from there. Pure function for testing.
