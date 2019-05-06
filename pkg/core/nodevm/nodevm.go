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
