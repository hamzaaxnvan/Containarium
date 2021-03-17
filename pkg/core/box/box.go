// Package box defines the runtime-neutral seam between the daemon and the
// substrate a box runs on. Today the only implementation is LXC/incus
// (pkg/core/box/lxc), which wraps the existing container.Manager with no
// behavior change. A Kubernetes implementation lands against this same
// contract behind a build tag — see docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md.
//
// The seam sits one altitude *above* incus.Backend on purpose: incus.Backend
// is a leaky, LXC-shaped interface (Exec, WriteFile, ResolveGPUInputToPCI,
// raw config-key writes) that a non-incus runtime cannot implement cleanly.
// BoxBackend is coarse-grained — lifecycle, addressing, SSH identity, and
// metadata — and lets each runtime realize those however it must.
package box

import (
	"context"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// BackendKind identifies the substrate a BoxBackend manages.
type BackendKind string

const (
	// KindLXC is the LXC/incus backend (today's default).
	KindLXC BackendKind = "lxc"
	// KindK8s is the Kubernetes backend (behind the `k8s` build tag).
	KindK8s BackendKind = "k8s"
)

// BoxRef names a box independent of runtime. Tenant is the routing key / SSH
// username the daemon's SSH front routes by; Name is the substrate-level
// object name (LXC: "<tenant>-container"; K8s: the StatefulSet pod "box-0").
// Name may be empty on input — an implementation derives it from Tenant via
// its own naming convention — and is always populated on values returned by
// the backend.
type BoxRef struct {
	Tenant string
	Name   string
}

// ResourceLimits is the runtime-neutral form of a box's CPU/memory/disk
// request. Values are substrate-native strings (e.g. "2", "4GB", "20GB"); an
// empty field means "leave unchanged" on mutation and "unset" on read.
type ResourceLimits struct {
	CPU    string
	Memory string
	Disk   string
	// StorageClass is the K8s StorageClass for the box's data PVC.
	// Empty = use the backend's default (Config.StorageClass).
	// Only meaningful on the K8s backend; ignored by LXC.
	StorageClass string
	// MemoryRequest is the K8s memory *request*, separate from Memory
	// (always applied as the limit). Empty = request equals Memory
	// (today's Guaranteed-QoS behavior). Only meaningful on the K8s
	// backend; ignored by LXC, which has no separate request concept.
	MemoryRequest string
	// CPURequest is the K8s CPU *request*, separate from CPU (always
	// applied as the limit). Same defaulting and K8s-only scope as
	// MemoryRequest.
	CPURequest string
}

// BoxSpec is the declarative input to Create. The backend makes a box matching
// the spec exist and returns a BoxStatus. Create must be idempotent on
// re-create (the agent-skills bring-up taught us this the hard way, #669).
type BoxSpec struct {
	NetworkTenant string // Explicit native NIC security domain; local Linux LXC only.
	Ref           BoxRef
	Image         string
	// Mode selects the box's SSH session behavior on backends that support it
	// (K8s → the agent-box image's AGENTBOX_MODE: "mcp" forced-command MCP, or
	// "shell" interactive login shell). Empty uses the backend's configured
	// default. Ignored by backends without the notion (LXC).
	Mode   string
	OSType pb.OSType
	// Isolation picks LXC container or QEMU/KVM VM (#2196). UNSPECIFIED
	// is the backend default (Windows → VM, else container). LXC only:
	// the K8s backend rejects VM, since a pod has no VM to offer.
	Isolation  pb.IsolationType
	Resources  ResourceLimits
	GPUs       []string // empty on K8s v1 (deferred)
	SSHKeys    []string
	Labels     map[string]string
	StaticIP   string // empty = DHCP
	Monitoring bool

	// StoragePool places the box's root disk on a named backend storage pool,
	// overriding the daemon-wide default for this create only. Empty keeps
	// today's behaviour exactly: the daemon's configured pool (#1213), or the
	// default profile when no disk size is requested either.
	//
	// It exists for per-tenant encryption (#1199): each tenant's containers
	// live on that tenant's own pool, sourced at that tenant's encrypted
	// dataset, because Incus clones the image to build an instance and a ZFS
	// clone inherits encryption from its origin rather than its location
	// (#1335). Placement is therefore per-request, not per-daemon.
	//
	// LXC only. The K8s backend expresses the same idea as
	// ResourceLimits.StorageClass and ignores this.
	StoragePool string

	// EnablePodman / EnablePodmanPrivileged request Docker/Podman support
	// inside the box (the privileged variant is gated by policy at the server
	// layer before it reaches here).
	EnablePodman           bool
	EnablePodmanPrivileged bool

	// Provisioning intent — NOT an exec script. The backend decides how to
	// realize it: LXC runs incus exec; K8s bakes it into the image / an init
	// container. Keeps stack-install runtime-specific, below the seam.
	Stack       string
	StackParams map[string]string

	// Git source provisioning (optional): the backend fetches the repo into
	// WorkspacePath at create time, no caller→box SSH.
	GitSource     string
	GitRef        string
	GitCredential string
	WorkspacePath string

	// Monitoring wiring, only meaningful when Monitoring is true. These are
	// daemon-runtime values the server supplies per-create (collector
	// endpoint, the originating backend's id for OTel resource attributes,
	// and the collector bearer). A K8s backend may ignore them.
	OTelCollectorEndpoint string
	OTelBackendID         string
	OTelBearer            string

	// AutoStart requests the box be started as part of Create.
	AutoStart bool

	// OnProvisioning, if set, is called once the box is running but still
	// installing packages/stack — the daemon uses it to flip async-create
	// state to "provisioning". A K8s backend may never call it.
	OnProvisioning func()
}

// BoxEndpoint describes how an agent reaches a box over SSH. The server turns
// it into Container.ssh_host and the displayed ssh command.
type BoxEndpoint struct {
	// SSHHost is the gateway/sentinel public host an agent connects to. Empty
	// means "direct IP mode" (no gateway in front) — the server may stamp the
	// daemon's configured gateway host above the seam when this is empty.
	SSHHost string
	// SSHPort is the gateway SSH port (22 via sshpiper); 0 means default.
	SSHPort int
	// SSHUser is the routing key the SSH front (sshpiper) routes by — the
	// tenant.
	SSHUser string
	// DirectIP is the box's own address, used when no gateway is in front.
	DirectIP string
	// AccessType distinguishes SSH from RDP (Windows VMs).
	AccessType pb.AccessType
