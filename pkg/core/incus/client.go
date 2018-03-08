package incus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	gopath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"golang.org/x/sync/errgroup"
)

// detectZFSContainersDataset checks if the "incus-local/containers" ZFS dataset exists.
func detectZFSContainersDataset() bool {
	cmd := exec.Command("zfs", "list", "-H", "-o", "name", "incus-local/containers") // #nosec G204 -- hardcoded args
	return cmd.Run() == nil
}

// Client wraps the Incus API client
type Client struct {
	server incus.InstanceServer

	// zfsGetPropertyFn and zfsSetPropertyFn replace host ZFS commands in unit
	// tests. Nil uses zfs get and zfs set respectively.
	zfsGetPropertyFn func(dataset, property string) (string, error)
	zfsSetPropertyFn func(dataset, property, value string) error

	// storagePolicy decides what EnsureStorage does when a pool does not
	// isolate tenant volumes (#1206). Zero value warns, which preserves the
	// pre-existing behaviour for dev hosts and single-tenant boxes.
	storagePolicy StoragePolicy

	// storageProbe overrides host detection during driver selection. Nil uses
	// DefaultStorageProbe.
	storageProbe *StorageProbe

	// storagePool is the incus storage pool containers are created on.
	// Empty means DefaultStoragePool (#1213).
	//
	// NOT the daemon's --pool, which names an independent cluster fronted by
	// the sentinel. These are unrelated concepts that share a word; see
	// docs/MULTI-POOL.md.
	storagePool string

	// socketPath is the Incus unix socket this client was connected to. Empty for clients built around a test
	// double. GetSystemResources uses it to share one bounded, cached read per socket (#2325).
	socketPath string
}

// DefaultStoragePool is the incus storage pool used when none is configured.
const DefaultStoragePool = "default"

// configuredStoragePool is the process-wide storage pool, set once from the
// daemon's --storage-pool flag.
//
// Process-wide because incus.New() is called from a dozen places that each
// build their own client — the cloud actuator, several container_server
// helpers — and none of them thread daemon configuration. A per-client
// setting alone would therefore be honoured on the daemon's own client and
// silently ignored by every other one, which is the same shape of bug this
// change exists to remove.
var configuredStoragePool atomic.Value // string

// SetDefaultStoragePool sets the process-wide incus storage pool. Called once
// during daemon startup, before any container is created. Empty is ignored.
//
// NOT the daemon's --pool, which names an independent cluster fronted by the
// sentinel; see docs/MULTI-POOL.md. These are unrelated concepts sharing a
// word, which is why the flag is --storage-pool.
func SetDefaultStoragePool(name string) {
	if name != "" {
		configuredStoragePool.Store(name)
	}
}

// ConfiguredStoragePool reports the process-wide pool: --storage-pool, or
// DefaultStoragePool when it was not set.
func ConfiguredStoragePool() string { return defaultPool() }

// defaultPool reports the process-wide pool.
func defaultPool() string {
	if v, ok := configuredStoragePool.Load().(string); ok && v != "" {
		return v
	}
	return DefaultStoragePool
}

// SetStoragePool selects the incus storage pool containers are created on.
// Empty resets to DefaultStoragePool. See #1213.
func (c *Client) SetStoragePool(name string) {
	c.storagePool = name
}

// StoragePool returns the configured incus storage pool.
//
// Every root-disk device construction goes through this rather than a literal
// "default". A literal is how a backend migrated onto an isolated pool
// silently un-migrated itself: existing tenants stayed put while every NEW
// tenant landed back on the shared-filesystem pool, so the contention probe
// went clean right after the migration and regressed weeks later with nothing
// in the product explaining why (#1206, #1213).
func (c *Client) StoragePool() string {
	if c.storagePool != "" {
		return c.storagePool
	}
	return defaultPool()
}

// SetStoragePolicy sets what EnsureStorage does when a storage pool does not
// isolate tenant volumes: warn (the default) or refuse. See #1206.
func (c *Client) SetStoragePolicy(p StoragePolicy) {
	c.storagePolicy = p
}

// probe returns the host storage probe, defaulting to the real one.
func (c *Client) probe() StorageProbe {
	if c.storageProbe != nil {
		return *c.storageProbe
	}
	return DefaultStorageProbe()
}

// Backend is the interface satisfied by *Client. Consumers depend on it (or a
// narrower subset declared at the call site) so they can be mocked in tests.
type Backend interface {
	// StoragePool is the incus storage pool containers are created on.
	// Read-only here: it is configured on the concrete client at daemon
	// startup, and every root-disk construction reads it rather than
	// hardcoding "default" (#1213).
	StoragePool() string

	// Lifecycle
	CreateContainer(config ContainerConfig) error
	StartContainer(name string) error
	StopContainer(name string, force bool) error
	DeleteContainer(name string) error
	GetContainer(name string) (*ContainerInfo, error)
	ListContainers() ([]ContainerInfo, error)
	WaitForNetwork(containerName string, timeout time.Duration) (string, error)

	// Exec & file I/O
	Exec(containerName string, command []string) error
	ExecWithOutput(containerName string, command []string) (string, string, error)
	// ExecWithExitCode is ExecWithOutput's sibling for a caller that needs
	// the command's exit status as data, not folded into err (SandboxService
	// ExecInSandbox, #1488: an agent inner loop branches on exit code as
	// normal control flow). err is non-nil only for a genuine
	// execution/transport failure — a non-zero exit is reported via
	// exitCode with err == nil.
	ExecWithExitCode(containerName string, command []string) (stdout, stderr string, exitCode int, err error)
	WriteFile(containerName, path string, content []byte, mode string) error
	ReadFile(containerName, path string) ([]byte, error)
	// ListDir returns the entry names of a directory inside the container,
	// via the incus file API — no exec, so nothing inside the box (a
	// tenant-controlled shell, say) is on the path. A missing directory is
	// an error; the caller decides whether that is "empty".
	ListDir(containerName, path string) ([]string, error)

	// Config & devices
	SetConfig(containerName, key, value string) error
	SetCPULimit(containerName, cpu string) error
	// SetCPUConfig writes limits.cpu and limits.cpu.allowance exactly as
	// given (empty unsets the key) — the raw setter the process responder
	// (#2636) uses to apply a throttle allowance while keeping the core
	// count, and to restore exactly what it recorded. SetCPULimit is the
	// translating resize path; this does no translation.
	SetCPUConfig(containerName, count, allowance string) error
	// FreezeContainer pauses a running instance (cgroup freezer): memory
	// and disk kept, no process scheduled until UnfreezeContainer.
	FreezeContainer(name string) error
	UnfreezeContainer(name string) error
	UnsetConfig(containerName, key string) error
	SetDeviceSize(containerName, deviceName, size string) error
	EnsureToolchainVolume(containerName, root string) error
	UpdateContainerConfig(name, key, value string) error
	GetRawInstance(name string) (map[string]string, string, error)
	ResolveGPUInputToPCI(input string) (string, error)
	CleanupDisk(containerName string) (string, int64, error)

	// Network ACLs + NIC devices. On the interface so the core-infra network
	// guard's reconciler (docs/architecture/core-infra-network-guard.md) can
	// be unit-tested against a fake. EnsureNICDevice exists because a
	// profile-inherited NIC has no instance-local device to hang
	// security.acls on; SetDeviceConfig merges per-device keys and is a
	// no-op when converged.
	GetNetworkACL(name string) (*api.NetworkACL, error)
	CreateNetworkACL(config ACLConfig) error
	UpdateNetworkACL(name string, config ACLConfig) error
	ListNetworkACLs() ([]api.NetworkACL, error)
	DeleteNetworkACL(name string) error
	AttachACLToContainer(containerName, aclName, deviceName string) error
	SetOwnedACL(containerName, aclName, deviceName, ownedPrefix string) error
	EnsureNICDevice(containerName string, want NICDevice) error
	SetDeviceConfig(containerName, deviceName string, keys map[string]string) error

	// GetConsoleLog returns the instance's boot-time console ring-buffer
	// log — a static read, not a live attach. Empty for an LXC container
	// (no serial console) or a VM that has produced no console output yet.
	GetConsoleLog(name string) (string, error)

	// Labels
	AddLabel(containerName, key, value string) error
	RemoveLabel(containerName, key string) error
	GetLabels(containerName string) (map[string]string, error)
	SetLabels(containerName string, labels map[string]string) error

	// Server info & metrics
	GetServerInfo() (*api.Server, error)
	GetContainerMetrics(name string) (*ContainerMetrics, error)

	// Image inspection (Phase 3.1 Phase-C)
	GetContainerImageFingerprint(containerName string) (string, error)

	// Baked base images (#1037): publish a provisioned container as a local
	// image under a stable alias (re-pointing the alias and reaping the
	// replaced image on re-bake), and read an alias's image properties so
	// the create fast-path can check what a baked image contains.
	PublishImage(containerName, alias string, properties map[string]string) (string, error)
	GetImageAliasProperties(alias string) (map[string]string, bool, error)

	// Whole-box export/import (#2801), streamed both ways. ExportInstance
	// works on a running instance and returns an Incus backup tarball
	// (point-in-time on ZFS); the caller must Close it. ImportInstance
	// creates a new, stopped instance on pool from such a tarball.
	ExportInstance(name string) (io.ReadCloser, error)
	ImportInstance(name, pool string, r io.Reader, opts ImportOptions) error
}

// DiskDevice represents a disk device configuration
type DiskDevice struct {
	Path string // Mount path (e.g., "/")
	Pool string // Storage pool name (e.g., "default")
	Size string // Disk size (e.g., "20GB")
}

// ToMap converts DiskDevice to the map format required by Incus API
func (d DiskDevice) ToMap() map[string]string {
	m := map[string]string{
		"type": "disk",
		"path": d.Path,
		"pool": d.Pool,
	}
	if d.Size != "" {
		m["size"] = d.Size
	}
	return m
}

// NICDevice represents a network interface device configuration
type NICDevice struct {
	Name        string // Interface name (e.g., "eth0")
	Network     string // Network name (e.g., "incusbr0")
	IPv4Address string // Static IPv4 address (empty for DHCP)
}

// ToMap converts NICDevice to the map format required by Incus API
func (n NICDevice) ToMap() map[string]string {
	m := map[string]string{
		"type":    "nic",
		"name":    n.Name,
		"network": n.Network,
	}
	if n.IPv4Address != "" {
		m["ipv4.address"] = n.IPv4Address
	}
	return m
}

// GPUDevice represents a GPU device configuration for passthrough
type GPUDevice struct {
	// ID is the GPU device ID (e.g., "0" for first GPU).
	// Leave empty to pass through all GPUs.
	//
	// Deprecated for new container creation: ID maps to Incus' DRM card
	// minor index, which can shift across kernel upgrades (e.g., the
	// 6.8.0-110 → 6.8.0-111 incident on a backend host where the RTX 4090
	// moved from card0 to card1, breaking every container with id="0").
	// Prefer PCI, which is stable across reboots and kernel changes.
	// New code should call ResolveGPUInputToPCI() to convert a
	// user-supplied ID into a PCI address before constructing this.
	ID string
	// PCI is the PCI address (e.g., "0000:0b:00.0") for a specific GPU.
	// Takes precedence over ID if set.
	PCI string
}

// pciMaskRawLXC hides the host's PCI device enumeration from a container's
// /proc and /sys, closing the hardware-fingerprinting leak described where
// it's applied (security#951). Live-validated on a real GPU host (RTX
// 3090): both mounts apply cleanly, the container restarts and remains
// otherwise fully functional, /proc/bus/pci/devices reads back empty, and
// /sys/bus/pci/devices/ lists zero entries — while an unmasked sibling
// container on the same host still shows the real device table.
const pciMaskRawLXC = "lxc.mount.entry = /dev/null proc/bus/pci/devices none bind,optional,create=file 0 0\n" +
	"lxc.mount.entry = tmpfs sys/bus/pci/devices tmpfs rw,size=1k,mode=0755,optional,create=dir 0 0"

// appendRawLXC adds lines to the container's raw.lxc config, appending to
// (rather than clobbering) any value already set by an earlier config
// decision — e.g. Docker-in-Docker's AppArmor override and the PCI-masking
// mount entries can both apply to the same container.
func appendRawLXC(cfg map[string]string, lines string) {
	if existing, ok := cfg["raw.lxc"]; ok && existing != "" {
		cfg["raw.lxc"] = existing + "\n" + lines
	} else {
		cfg["raw.lxc"] = lines
	}
}

// gpuDeviceName returns the Incus device name for the i-th attached GPU.
// The first GPU keeps the legacy "gpu" name so single-GPU containers are
// byte-identical to the pre-multi-GPU config; subsequent GPUs are "gpu1",
// "gpu2", ... Device names must be unique within a container.
func gpuDeviceName(i int) string {
	if i == 0 {
		return "gpu"
	}
	return fmt.Sprintf("gpu%d", i)
}

// gpuDeviceIdentity extracts a human-readable identity for a GPU device
// from its Incus device config map — the PCI address if pinned (preferred,
// stable), else the DRM id, else a generic "GPU" marker.
func gpuDeviceIdentity(device map[string]string) string {
	if pci, ok := device["pci"]; ok && pci != "" {
		return pci
	}
	if id, ok := device["id"]; ok && id != "" {
		return id
	}
	return "GPU" // Generic GPU indicator (pass-through-all, no pin)
}

// finalizeGPUInfo sorts the collected GPU identities for stable output and
// derives the single-value GPU field (first entry) for display/back-compat.
// Incus returns devices as an unordered map, so sorting is what makes the
// read-back deterministic.
func finalizeGPUInfo(info *ContainerInfo) {
	if len(info.GPUs) == 0 {
		return
	}
	sort.Strings(info.GPUs)
	info.GPU = info.GPUs[0]
}

// ToMap converts GPUDevice to the map format required by Incus API
func (g GPUDevice) ToMap() map[string]string {
	m := map[string]string{
		"type": "gpu",
	}
	if g.PCI != "" {
		m["pci"] = g.PCI
	} else if g.ID != "" {
		m["id"] = g.ID
	}
	return m
}

// ResolveGPUInputToPCI converts a user-supplied GPU identifier (typically
// "0", "1", or a PCI address like "0000:0b:00.0") into a PCI address by
// looking up the system's GPU list. Returns the original input unchanged
// when it's already PCI-shaped. Returns an error if the input is a
// numeric index out of range, or if no GPUs are detected.
//
// Why we do this: Incus' "id" field for GPU devices is the DRM card
// minor (the digit at the end of /dev/dri/cardN). That index can shift
// across kernel upgrades when the kernel's DRM enumeration order
// changes — the resulting "Failed to detect requested GPU device"
// error is silent (no preflight) and only surfaces on the next
// container start. PCI addresses are stable, so we resolve before
// writing the device config.
func (c *Client) ResolveGPUInputToPCI(input string) (string, error) {
	// Pure-function part is testable without an Incus daemon.
	// If the input is already PCI-shaped, no daemon call needed.
	if pci, ok, err := classifyGPUInput(input); err != nil {
		return "", err
	} else if ok {
		return pci, nil
	}
	// Numeric index path requires enumerating system GPUs.
	idx, _ := strconv.Atoi(input) // already validated by classifyGPUInput
	res, err := c.GetSystemResources()
	if err != nil {
		return "", fmt.Errorf("GPU input %q: failed to enumerate system GPUs: %w", input, err)
	}
	return resolveGPUByIndex(input, idx, res.GPUs)
}

// classifyGPUInput returns (pci, true, nil) if input is already a PCI
// address; (empty, false, nil) if it's a valid numeric index needing
// resolution; (empty, false, err) for invalid inputs (empty / bad format).
func classifyGPUInput(input string) (string, bool, error) {
	if input == "" {
		return "", false, fmt.Errorf("empty GPU input")
	}
	if strings.Contains(input, ":") {
		// "0000:0b:00.0" — pass through unchanged
		return input, true, nil
	}
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 0 {
		return "", false, fmt.Errorf("GPU input %q: not a PCI address or non-negative integer", input)
	}
	_ = idx
	return "", false, nil
}

// resolveGPUByIndex picks the Nth GPU's PCI address from a list.
func resolveGPUByIndex(input string, idx int, gpus []GPUInfo) (string, error) {
	if idx >= len(gpus) {
		return "", fmt.Errorf("GPU input %q: index %d out of range (system has %d GPU(s))", input, idx, len(gpus))
	}
	pci := gpus[idx].PCIAddress
	if pci == "" {
		return "", fmt.Errorf("GPU input %q: GPU at index %d has no PCI address (vendor=%q, model=%q)", input, idx, gpus[idx].Vendor, gpus[idx].Model)
	}
	return pci, nil
}

// ContainerConfig holds configuration for creating a container
type ContainerConfig struct {
	// ManagedToolchainRoot is the installed host toolchain directory, if any.
	ManagedToolchainRoot string

	Name                   string
	Image                  string
	CPU                    string
	Memory                 string
	Disk                   *DiskDevice      // Root disk configuration
	NIC                    *NICDevice       // Network interface configuration
	GPUs                   []GPUDevice      // GPU devices for passthrough (one per attached GPU; empty = none)
	InstanceType           api.InstanceType // Container (LXC) or VM (QEMU/KVM). Defaults to Container.
	EnableNesting          bool
	EnablePodmanPrivileged bool // Full Docker support (requires privileged container + AppArmor disabled)
	AutoStart              bool

	// ExtraConfig is applied verbatim as Incus instance config keys,
	// after the fields above. Used by the cluster container-node
	// profile (#1429: security.nesting for k3s node containers);
	// callers own the keys they set.
	ExtraConfig map[string]string

	// Env is a map of environment variables to set inside the
	// container, equivalent to `incus config set <name>
	// environment.<KEY> <value>`. Visible to every shell session and
	// to systemd-managed processes. Used today for the OTel
	// app-monitoring opt-in (OTEL_EXPORTER_OTLP_ENDPOINT etc.) and
	// can host other platform-injected env vars going forward.
	Env map[string]string
}

// LabelPrefix is the prefix used for storing labels in Incus container config
// Note: Incus requires user-defined config keys to use the "user." prefix
const LabelPrefix = "user.containarium.label."

// RoleKey is the Incus config key used to tag core containers by role.
const RoleKey = "user.containarium.role"

// TenantLabelKey is the Incus config key that names a container's owning tenant
// explicitly, decoupling tenant identity from the <tenant>-container naming
// convention. The network-policy enforcer (#315) prefers this label and falls
// back to the name. Stamped by the cloud-actuation reconciler from the
// assignment (multi-tenant cloud), or by an operator for a container that
// doesn't follow the naming convention. See NETWORK-ISOLATION-DESIGN.md
// "Cloud extension".
const TenantLabelKey = "user.containarium.tenant"

// CloudOrgIDLabel is the attribution label (under LabelPrefix) the hosted
// control plane stamps on every container it creates via the OSS-primary
// driver — the owning org UUID. ResolveTenant reads it as the second
// source of tenant identity.
const CloudOrgIDLabel = "cloud_org_id"

// ContainerNameSuffix is the <tenant>-container naming convention's suffix.
const ContainerNameSuffix = "-container"

// ResolveTenant determines a container's owning tenant, in the precedence
// every tenant-scoped control shares (the eBPF enforcer, the tenant guard):
// the explicit tenant label (TenantLabelKey) wins; then the cloud_org_id
// attribution label; then the <tenant>-container naming convention. ""
// when none yields a tenant — the container is then left unmanaged and
// reported, never guessed. Account and shared-domain identities stay distinct.
func ResolveTenant(tenantLabel, cloudOrgID, containerName string) string {
	if t := strings.TrimSpace(tenantLabel); t != "" {
		return t
	}
	if t := strings.TrimSpace(cloudOrgID); t != "" {
		return t
	}
	if !strings.HasSuffix(containerName, ContainerNameSuffix) {
		return ""
	}
	return strings.TrimSuffix(containerName, ContainerNameSuffix)
}

// Role is a typed string for core container roles.
type Role string

// Core container roles
const (
	RoleNone            Role = ""
	RolePostgres        Role = "core-postgres"
	RoleCaddy           Role = "core-caddy"
	RoleVictoriaMetrics Role = "core-victoriametrics"
	RoleSecurity        Role = "core-security"
	RoleGuacamole       Role = "core-guacamole"
	RoleOTelCollector   Role = "core-otelcollector"
	// RoleControlPlane marks a control-plane container (e.g. a cloud control
	// plane co-located on a backend host). It is platform infrastructure, not a
	// tenant: tenants legitimately call its auth-gated API, and it must reach
	// every box to actuate them. The network-policy enforcer excludes it from
	// tenant tagging so it's reachable as an external destination and its own
	// egress is unenforced (#780). Distinct from the co-tenant core services
	// above, which stay tenant-isolated.
	RoleControlPlane Role = "core-controlplane"
)

// IsCoreRole returns true if the role represents a core container.
func (r Role) IsCoreRole() bool {
	return r != RoleNone
}

// ContainerInfo holds information about a container
type ContainerInfo struct {
	Name string
	// Username is the SSH login the daemon assigned to this container. It
	// may differ from the requested name (e.g. a control plane that mints a
	// generated username at create time), and it's what the daemon's SSH
	// front routes by — so callers doing SSH/install must use this, not the
	// requested name. Empty when the daemon doesn't report it.
	Username  string
	State     string
	IPAddress string
	// SSHHost is the host a client should SSH to, as computed by the
	// daemon from its own routing config (proto Container.ssh_host): the
	// container IP in direct mode, the sentinel's public host when routed
	// through one. Empty when the daemon doesn't report it (local Incus,
	// older daemons) — callers then fall back to IPAddress.
	SSHHost             string
	CPU                 string
	Memory              string
	Disk                string
	GPU                 string   // First GPU device info, for display/back-compat (e.g., PCI address or GPU ID); empty if none
	GPUs                []string // All attached GPU devices (PCI address or ID per entry), sorted for stable output
	InstanceType        string   // "container" or "virtual-machine"
	Labels              map[string]string
	Role                Role                // Core container role (e.g., RolePostgres, RoleCaddy), empty for user containers
	NetworkTenantSource NetworkTenantSource // Populated by remote API clients.
	Tenant              string              // Explicit owning tenant (user.containarium.tenant); empty falls back to the name convention
	// Managed is true when the instance carries any user.containarium.* config
	// key, i.e. Containarium created or adopted it. An instance made directly
	// with `incus launch` has none.
	Managed   bool
	CreatedAt time.Time
	BackendID string // Backend this container runs on (populated by PeerPool fan-out)
	// Pid is the container's init process id in the HOST pid namespace, from
	// the same InstanceState read that supplies IPAddress; 0 when the
	// container is not running or the state read failed. The exec-telemetry
	// watcher (#2637) resolves /proc/<Pid>/cgroup from it.
	Pid int64

	// MonitoringEnabled is true when the container has OTel
	// env vars stamped (OTEL_EXPORTER_OTLP_ENDPOINT non-empty in
	// the Incus environment config). Derived at read-time from
	// the Incus config map rather than tracked as a separate
	// flag — single source of truth.
	MonitoringEnabled bool

	// Stack mirrors user.containarium.stack: the stack requested at create.
	// Empty when none was requested or the box predates the key.
	Stack string

	// PodmanEnabled mirrors user.containarium.podman_enabled: whether the box
	// was created with podman support. True for boxes that predate the key.
	PodmanEnabled bool

	// AutoSleepEnabled mirrors user.containarium.auto_sleep_enabled
	// on the Incus config — opt-in flag for the serverless feature.
	AutoSleepEnabled bool

	// IdleThresholdMinutes mirrors user.containarium.idle_threshold_minutes;
	// defaults to 15 when the key is missing or unparseable.
	IdleThresholdMinutes int32

	// LastStartedAt mirrors user.containarium.last_started_at, stamped by
	// StartContainer. Zero value when the key is missing or unparseable.
	// Consumed by the Phase 2 auto-sleep ticker for its anti-thrash window.
	LastStartedAt time.Time

	// TTLExpiresAt mirrors user.containarium.ttl_expires_at — the wall-clock
	// time at which the daemon's ttlsweeper goroutine should force-delete
	// the container. Zero value when the key is missing or unparseable
	// (treated by the sweeper and proto-conversion as "no TTL set").
	// Persistence model is the Incus container config (survives daemon
	// restart, no extra store needed); see internal/ttlsweeper for the
	// sweep side and server.SetContainerTTL for the writer side.
	TTLExpiresAt time.Time

	// StoppedAt mirrors user.containarium.stopped_at — when the box most
	// recently became STOPPED (cleared on start). Zero when running or
	// unknown. The two-phase reaper measures the stopped→delete window from
	// here (#525).
	StoppedAt time.Time

	// DeleteAfterStoppedSeconds mirrors
	// user.containarium.delete_after_stopped_seconds — the per-box
	// stopped→delete window (#525). 0 = never delete on stop. Independent of
	// auto-sleep so a scale-to-zero box that merely sleeps is never reaped.
	DeleteAfterStoppedSeconds int64

	// DeletePolicy mirrors user.containarium.delete_policy (#284). When set to
	// DeletePolicyProtected, every automated/bulk deletion path (ttlsweeper
	// auto-reap, `containarium prune`) skips the box — only a deliberate
	// single-box delete can remove it. Empty = unprotected (today's default).
	DeletePolicy string

	// ToolAccess mirrors user.containarium.tool_access (#2944): the level
	// recorded for platform-started agent runs (ToolAccessNone,
	// ToolAccessBuildTools, ToolAccessFullSudo). Empty = none recorded, and
	// those runs keep today's behaviour.
	ToolAccess string

	// Image is a human-readable description of the image the container was
	// launched from (Incus's auto-populated image.description config key,
	// falling back to the volatile.base_image fingerprint). Empty if Incus
	// never recorded either — e.g. a container created by a very old client.
	Image string
}

// AutoSleepEnabledKey is the Incus config key storing the per-container
// auto-sleep opt-in flag (Phase 1 of the serverless feature).
const AutoSleepEnabledKey = "user.containarium.auto_sleep_enabled"

// IdleThresholdMinutesKey is the Incus config key storing the per-container
// idle threshold in minutes consumed by the Phase 2 auto-sleep ticker.
const IdleThresholdMinutesKey = "user.containarium.idle_threshold_minutes"

// LastStartedAtKey is the Incus config key storing the RFC3339 timestamp of
// the most recent StartContainer success. The Phase 2 auto-sleep ticker
// reads it to enforce its anti-thrash window (don't sleep a container
// within 2× the idle threshold of its last start).
const LastStartedAtKey = "user.containarium.last_started_at"

// TTLExpiresAtKey is the Incus config key storing the RFC3339 timestamp at
// which the container should be auto-deleted. Written by SetContainerTTL
// (PR following #299) and read by the ttlsweeper goroutine on every tick.
// Persisted on the Incus config (not a separate store) so it survives
// daemon restart with no extra plumbing.
const TTLExpiresAtKey = "user.containarium.ttl_expires_at"

// StoppedAtKey is the Incus config key storing the RFC3339 timestamp at
// which the container most recently transitioned to STOPPED. Written by
// StopContainer and cleared by StartContainer, so it measures how long a
// box has been continuously stopped — the clock the two-phase reaper's
// stopped→delete timer runs against (#525). Waking the box (start) clears
// it, which resets that timer.
const StoppedAtKey = "user.containarium.stopped_at"

// DeleteAfterStoppedSecondsKey is the Incus config key storing the
// per-container stopped→delete window in seconds (#525). When set (> 0) and
// the box has been STOPPED for longer than this, the ttlsweeper deletes it —
// reclaiming disk after the idle→stop step (#524) already reclaimed CPU/RAM.
// This is a SEPARATE opt-in from auto_sleep_enabled: a scale-to-zero box that
// merely sleeps must never be deleted just for being stopped; only a box that
// explicitly asked for stopped→delete gets reaped. Absent/0 = never delete on
// stop (today's behavior).
const DeleteAfterStoppedSecondsKey = "user.containarium.delete_after_stopped_seconds"

// StackKey is the Incus config key recording the software stack a box was
// created with (--stack), so list/get can report it after creation (#2163).
// Absent = no stack requested, or a box created before this key existed.
const StackKey = "user.containarium.stack"

// PodmanEnabledKey is the Incus config key recording whether a box was created
// with podman support (enable_podman), "true" or "false" (#2858). It is
// written on every create so a box created with podman off is distinguishable
// from one that predates the key. Absent = created before this key existed;
// those boxes keep reporting podman enabled, which is what status reported
// for every box before the key (see podmanEnabledFromConfig).
const PodmanEnabledKey = "user.containarium.podman_enabled"

// podmanEnabledFromConfig reads PodmanEnabledKey. Absent or unparseable means
// a box created before the key existed and reports true, the value status
// always returned for it; only an explicit "false" reports false.
func podmanEnabledFromConfig(cfg map[string]string) bool {
	v, err := strconv.ParseBool(cfg[PodmanEnabledKey])
	if err != nil {
		return true
	}
	return v
}

// DeletePolicyKey is the Incus config key carrying a box's delete policy
// (#284). A box set to DeletePolicyProtected is skipped by every automated /
// bulk deletion path — the ttlsweeper's auto-reap and `containarium prune` —
// so a "clean up leaked boxes" sweep can never take out a persistent runner.
// Absent/empty = unprotected (today's default: eligible for prune + reap).
const DeletePolicyKey = "user.containarium.delete_policy"

// DeletePolicyProtected marks a box that must never be auto-reaped or
// bulk-deleted (e.g. a persistent GitHub Actions runner). Deleting it takes a
// deliberate single-box delete, not a sweep. The value is a stable string
// contract: operators can set it directly (`incus config set <box>
// user.containarium.delete_policy protected`) and every deletion path honors it.
const DeletePolicyProtected = "protected"

// ToolAccessKey is the Incus config key carrying the tool-access level for
// platform-started (unattended) agent runs on a box (#2944). Absent = none
// recorded: those runs keep today's behaviour. Operators can set it directly
// (`incus config set <box> user.containarium.tool_access build_tools`).
const ToolAccessKey = "user.containarium.tool_access"

// The recorded tool-access values. A stable string contract, like
// DeletePolicyProtected; the proto enum BoxToolAccess is the typed API.
const (
	ToolAccessNone       = "none"
	ToolAccessBuildTools = "build_tools"
	ToolAccessFullSudo   = "full_sudo"
)

// DefaultIdleThresholdMinutes is the fallback used when the threshold
// config key is missing or unparseable.
const DefaultIdleThresholdMinutes = 15

// parseStoppedAt reads the stopped-at timestamp from an Incus config map.
// Missing/unparseable → zero time ("not known to be stopped"), so a corrupt
// key never trips a false-positive delete.
func parseStoppedAt(cfg map[string]string) time.Time {
	raw, ok := cfg[StoppedAtKey]
	if !ok || raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseDeleteAfterStoppedSeconds reads the stopped→delete window. Missing,
// unparseable, or <= 0 → 0, which the reaper treats as "no stopped→delete"
// (the safe default — never reap a stopped box unless it opted in).
func parseDeleteAfterStoppedSeconds(cfg map[string]string) int64 {
	raw, ok := cfg[DeleteAfterStoppedSecondsKey]
	if !ok || raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseIdleThresholdMinutes reads the threshold key from an Incus
// config map, falling back to the default for empty/garbage values.
func parseIdleThresholdMinutes(cfg map[string]string) int32 {
	raw, ok := cfg[IdleThresholdMinutesKey]
	if !ok || raw == "" {
		return DefaultIdleThresholdMinutes
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return DefaultIdleThresholdMinutes
	}
	return safecast.I32(n)
}

// parseLastStartedAt reads the last-started timestamp from an Incus config
// map. Missing or unparseable values yield the zero time — callers treat
// that as "unknown" rather than a real moment in epoch history.
func parseLastStartedAt(cfg map[string]string) time.Time {
	raw, ok := cfg[LastStartedAtKey]
	if !ok || raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseTTLExpiresAt reads the TTL-expiry timestamp from an Incus config
// map. Missing or unparseable values yield the zero time — the read-path
// (toProtoContainer) and the sweeper both treat the zero value as "no
// TTL set" so a corrupt key never 5xx's the list endpoint or trips
// false-positive deletions.
func parseTTLExpiresAt(cfg map[string]string) time.Time {
	raw, ok := cfg[TTLExpiresAtKey]
	if !ok || raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ContainerMetrics holds runtime metrics for a container
type ContainerMetrics struct {
	Name             string
	CPUUsageSeconds  int64 // CPU usage in seconds
	MemoryUsageBytes int64 // Current memory usage in bytes
	MemoryLimitBytes int64 // Memory limit in bytes
	DiskUsageBytes   int64 // Root disk usage in bytes
	NetworkRxBytes   int64 // Network bytes received
	NetworkTxBytes   int64 // Network bytes transmitted
	ProcessCount     int32 // Number of running processes

	// CFS bandwidth-throttling counters from the container's cgroup cpu.stat,
	// cumulative like CPUUsageSeconds. All zero means "no signal available"
	// (see cpuThrottle in cpu_stat.go), not "never throttled".
	CPUNrPeriods     int64 // CFS periods elapsed
	CPUNrThrottled   int64 // CFS periods in which the container was throttled
	CPUThrottledUsec int64 // Total time spent throttled, in microseconds
}

// ServerInfo holds information about the Incus server
type ServerInfo struct {
	Version       string
	KernelVersion string
	// Memory totals reported by the daemon. A zero total means unreported
	// (local mode or an older daemon), so the CLI omits the memory block.
	TotalMemoryBytes     int64
	AvailableMemoryBytes int64
	// CPUBudget is the daemon's tenant / core / physical CPU breakdown and
	// admission-gate posture (#2284). Nil when the source did not report
	// one — local Incus mode, or a daemon that predates the field.
	CPUBudget *CPUBudget
	// StoragePools is each storage pool's fill as the daemon's capacity
	// monitor last read it (#2823). Nil in local mode or from an older daemon.
	StoragePools []StoragePoolCapacity
}

// DefaultSocketPath is the Incus unix socket path used by New() and by any
// caller that wants the same default New() uses while still picking its
// own constructor variant (e.g. NewWithSocketAndTimeout).
const DefaultSocketPath = "/var/lib/incus/unix.socket"

// New creates a new Incus client
// Connects to the local Incus daemon via Unix socket
func New() (*Client, error) {
	return NewWithSocket(DefaultSocketPath)
}

// NewWithSocket creates a new Incus client with a specific socket path.
//
// This client has no per-call HTTP timeout (nil ConnectionArgs leaves
// http.Client.Timeout at its zero value, i.e. unbounded) — every other
// daemon code path uses this deliberately, since legitimate long-running
// operations (image pulls, container creation, migrations, ...) go through
// it. Do not add a timeout here; use NewWithSocketAndTimeout for a call
// site that specifically needs one (#1483).
func NewWithSocket(socketPath string) (*Client, error) {
	server, err := incus.ConnectIncusUnix(socketPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Incus: %w", err)
	}

	return &Client{server: server, socketPath: socketPath}, nil
}

// NewWithSocketAndTimeout creates a new Incus client whose every HTTP call
// over the unix socket is bounded by timeout (via http.Client.Timeout).
//
// Why this exists (#1483): the incus SDK's InstanceServer interface has no
// per-call context.Context parameter, so a per-call deadline can only be
// set once, at construction, via ConnectionArgs.HTTPClient. On a byoc node
// where incusd is still busy initializing storage right after boot, a
// single GetInstance/GetInstanceNames round-trip made through the
// unbounded client NewWithSocket returns can block for as long as the
// SDK's own transport-level ResponseHeaderTimeout (3600s) — far past any
// reasonable "is it ready yet" polling loop's outer bound, and long enough
// that the loop never gets to retry because the goroutine is parked
// inside that one call rather than spinning through the loop.
//
// This is intentionally scoped to callers that can tolerate a bounded call
// failing and simply retrying (e.g. the daemon's core-container boot-readiness
// wait) — not a replacement for the shared, long-lived client used
// elsewhere, which legitimately needs to support slow, long-running
// operations without an artificial deadline cutting them off.
func NewWithSocketAndTimeout(socketPath string, timeout time.Duration) (*Client, error) {
	server, err := incus.ConnectIncusUnix(socketPath, &incus.ConnectionArgs{
		HTTPClient: &http.Client{Timeout: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Incus: %w", err)
	}

	return &Client{server: server, socketPath: socketPath}, nil
}

// parseImageSource parses an image string and returns the appropriate InstanceSource
// Handles formats like:
//   - "images:ubuntu/24.04" -> remote from images.linuxcontainers.org
//   - "ubuntu/24.04" -> defaults to images.linuxcontainers.org (most common)
//   - "ubuntu:24.04" -> remote from cloud-images.ubuntu.com
//   - "ubuntu" -> local alias
func parseImageSource(image string) api.InstanceSource {
	source := api.InstanceSource{
		Type: "image",
	}

	// Check if it's a remote image (contains ":")
	if strings.Contains(image, ":") {
		parts := strings.SplitN(image, ":", 2)
		remote := parts[0]
		alias := parts[1]

		// Map common remote names to their URLs
		switch remote {
		case "images":
			source.Server = "https://images.linuxcontainers.org"
			source.Protocol = "simplestreams"
			source.Alias = alias
		case "ubuntu":
			source.Server = "https://cloud-images.ubuntu.com/releases"
			source.Protocol = "simplestreams"
			source.Alias = alias
		case "ubuntu-daily":
			source.Server = "https://cloud-images.ubuntu.com/daily"
			source.Protocol = "simplestreams"
			source.Alias = alias
		default:
			// Unknown remote, try as local alias
			source.Alias = image
		}
	} else if strings.Contains(image, "/") {
		// Format like "ubuntu/24.04" without remote prefix
		// Default to images.linuxcontainers.org which is most commonly used
		source.Server = "https://images.linuxcontainers.org"
		source.Protocol = "simplestreams"
		source.Alias = image
	} else {
		// Simple name like "ubuntu" - treat as local alias
		source.Alias = image
	}

	return source
}

// CreateContainer creates a new container with the specified configuration
func (c *Client) CreateContainer(config ContainerConfig) error {
	// Debug: Log the image being used
	fmt.Printf("[DEBUG] CreateContainer - Image: '%s'\n", config.Image)

	// Prepare instance creation request
	instanceType := config.InstanceType
	if instanceType == "" {
		instanceType = api.InstanceTypeContainer
	}
	req := api.InstancesPost{
		Name: config.Name,
		Type: instanceType,
	}

	// Parse image source - handle remote images like "images:ubuntu/24.04"
	req.Source = parseImageSource(config.Image)

	// Debug: Log the parsed source
	fmt.Printf("[DEBUG] CreateContainer - Source: Type=%s, Server=%s, Protocol=%s, Alias=%s\n",
		req.Source.Type, req.Source.Server, req.Source.Protocol, req.Source.Alias)

	// Set container configuration
	req.Config = make(map[string]string)

	// Resource limits. CPU may be a whole-core count, a CPU set, or a
	// fractional request (millicpu / decimal) — translate to the correct
	// Incus key(s): limits.cpu always, plus limits.cpu.allowance for the
	// CFS-bandwidth throttle on numeric (non-CPU-set) requests. See
	// issues #401 and #1029.
	if config.CPU != "" {
		cl, err := parseCPULimit(config.CPU)
		if err != nil {
			return err
		}
		if cl.Count != "" {
			req.Config["limits.cpu"] = cl.Count
		}
		if cl.Allowance != "" {
			req.Config["limits.cpu.allowance"] = cl.Allowance
		}
	}
	if config.Memory != "" {
		req.Config["limits.memory"] = config.Memory
	}

	// Docker support (nesting)
	if config.EnableNesting {
		req.Config["security.nesting"] = "true"
		req.Config["security.syscalls.intercept.mknod"] = "true"
		req.Config["security.syscalls.intercept.setxattr"] = "true"
	}

	// Full Docker-in-Docker support (privileged mode with AppArmor disabled)
	// This is required for Docker to run properly inside the container
	if config.EnablePodmanPrivileged {
		req.Config["security.privileged"] = "true"
		appendRawLXC(req.Config, "lxc.apparmor.profile=unconfined")
	}

	// PCI device enumeration isn't namespaced by the Linux kernel (unlike
	// network/mount/PID), so even an unprivileged container with no GPU
	// device attached can read /proc/bus/pci/devices and
	// /sys/bus/pci/devices to fingerprint the host's hardware — including
	// whether it has a GPU and its exact model (security#951). Mask both
	// for containers that weren't granted a GPU device; a GPU-passthrough
	// container legitimately needs to see its own device, so this only
	// applies when no GPU was requested.
	if len(config.GPUs) == 0 {
		appendRawLXC(req.Config, pciMaskRawLXC)
	}

	// Auto-start on boot
	if config.AutoStart {
		req.Config["boot.autostart"] = "true"
	}

	// Caller-owned instance config (see ExtraConfig).
	for k, v := range config.ExtraConfig {
		req.Config[k] = v
	}

	// Environment variables — Incus stores these as `environment.<KEY>`
	// entries in the config map. Visible to login shells and to
	// systemd-managed processes inside the container.
	for k, v := range config.Env {
		req.Config["environment."+k] = v
	}

	// Device configuration
	req.Devices = make(map[string]map[string]string)

	// Root disk device
	if config.Disk != nil {
		req.Devices["root"] = config.Disk.ToMap()
		fmt.Printf("[DEBUG] CreateContainer - Disk: path=%s, pool=%s, size=%s\n",
			config.Disk.Path, config.Disk.Pool, config.Disk.Size)
	}

	// Network interface device
	if config.NIC != nil {
		req.Devices[config.NIC.Name] = config.NIC.ToMap()
		if config.NIC.IPv4Address != "" {
			fmt.Printf("[DEBUG] CreateContainer - NIC: name=%s, network=%s, static_ip=%s\n",
				config.NIC.Name, config.NIC.Network, config.NIC.IPv4Address)
		}
	}

	// GPU device passthrough. Each attached GPU is a distinct Incus device:
	// the first keeps the legacy name "gpu" (byte-identical to the old
	// single-GPU config), the rest are "gpu1", "gpu2", ... — Incus device
	// names must be unique within a container.
	for i, g := range config.GPUs {
		name := gpuDeviceName(i)
		req.Devices[name] = g.ToMap()
		fmt.Printf("[DEBUG] CreateContainer - GPU[%s]: id=%s, pci=%s\n", name, g.ID, g.PCI)
	}

	// Create the container
	if config.ManagedToolchainRoot != "" {
		req.Devices["toolchains"] = toolchainDevice(config.ManagedToolchainRoot)
	}
	op, err := c.server.CreateInstance(req)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Wait for operation to complete
	if err := op.Wait(); err != nil {
		return fmt.Errorf("failed to create container (operation failed): %w", err)
	}

	return nil
}

// StartContainer starts a container
