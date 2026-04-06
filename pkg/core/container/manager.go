package container

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ospkg"
	"github.com/footprintai/containarium/pkg/core/ostype"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
	"github.com/footprintai/containarium/pkg/core/stacks"
	"github.com/footprintai/containarium/pkg/core/toolchain"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	incusapi "github.com/lxc/incus/v7/shared/api"
)

// Manager handles container lifecycle operations
type Manager struct {
	incus         incus.Backend
	toolchainRoot string // test override; production uses toolchain.HostRoot
	// mirrors are the operator-configured registry pull-through mirrors (#908)
	// injected into each podman-enabled box's registries.conf.d. Empty = boxes
	// pull from upstream registries as before. See registry_mirror.go.
	mirrors []RegistryMirror
	// observeStage, when non-nil, receives per-stage create latency.
	// See create_stages.go.
	observeStage StageObserver
	// cloudInitWait bounds installPackages' post-boot wait for cloud-init to
	// settle, when the image actually has cloud-init (#1530). Zero (the
	// default, production) uses defaultCloudInitWait; a test seam so a test
	// exercising the wait itself doesn't cost the real 5s.
	cloudInitWait time.Duration
	// nicGuard, when non-nil, guards a new container's NIC between create
	// and start (see NICGuard). Nil = no hook.
	nicGuard NICGuard
	// nameCache backs ExistingContainerNames — see its doc comment.
	nameCache containerNameCache
	// nameCacheTTL overrides existingContainerNamesCacheTTL. Zero (the
	// default, production) uses the real constant; a test seam so a test
	// exercising staleness doesn't need to sleep the real TTL.
	nameCacheTTL time.Duration
}

// defaultCloudInitWait is installPackages' production wait when cloud-init
// is present (Manager.cloudInitWait unset).
const defaultCloudInitWait = 5 * time.Second

// CreateOptions holds options for creating a container
type CreateOptions struct {
	// NetworkTenant is an explicit, administrator-authorized NIC security domain.
	// It is independent of the encryption tenant_id and of cloud labels.
	NetworkTenant string
	Username      string
	Image         string
	CPU           string
	Memory        string
	Disk          string   // Disk size (e.g., "20GB")
	GPUs          []string // GPU device IDs for passthrough — one per attached GPU ("0"/"1" or PCI address); empty = none
	StaticIP      string   // Static IP address (e.g., "10.100.0.100") - empty for DHCP

	// StoragePool overrides the daemon-wide storage pool for this create.
	// Empty uses the configured pool (#1213). See box.BoxSpec.StoragePool for
	// why placement is per-request: per-tenant encryption puts each tenant's
	// containers on that tenant's own pool (#1199).
	StoragePool string

	SSHKeys                []string
	Labels                 map[string]string // Kubernetes-style labels
	EnablePodman           bool
	EnablePodmanPrivileged bool // Full Docker support (privileged + AppArmor disabled)
	AutoStart              bool
	Verbose                bool
	Stack                  string            // Software stack to install (e.g., "nodejs", "python")
	StackParameters        map[string]string // Stack parameters — passed to install scripts as CONTAINARIUM_STACK_<name> env vars
	OSType                 pb.OSType         // Operating system type for the container
	Isolation              pb.IsolationType  // Container (LXC) or VM; UNSPECIFIED = Windows→VM, else container (#2196)
	OnProvisioning         func()            // Called when container is running but still provisioning (installing packages/stack)
	RDPPassword            string            // Generated RDP password for Windows VMs (output, set by Create)

	// Monitoring toggles application-emitted OpenTelemetry. When
	// true, the container is created with OTEL_EXPORTER_OTLP_ENDPOINT
	// and related env vars pointing at the core OTel collector LXC,
	// so any OTel SDK inside ships telemetry without app-side config.
	// Default false; see docs/OTEL-COLLECTOR-DESIGN.md.
	Monitoring bool

	// OTelCollectorEndpoint is the collector's OTLP/HTTP endpoint
	// (e.g. "http://10.0.3.142:4318"). Only used when Monitoring=true.
	// Empty + Monitoring=true is logged as a warning and treated as
	// "don't inject env vars" — the collector isn't running on this
	// daemon, so injecting a dead endpoint helps no one.
	OTelCollectorEndpoint string

	// BackendID is this daemon's backend ID, stamped into
	// OTEL_RESOURCE_ATTRIBUTES so cross-VM dashboards can filter by
	// the VM that emitted the metric. Only used when Monitoring=true.
	BackendID string

	// OTelBearer is the per-deployment shared secret stamped onto
	// monitoring=true containers as OTEL_EXPORTER_OTLP_HEADERS=
	// Authorization=Bearer <secret>. Empty omits the header
	// (pre-Phase-2.5 behavior; the collector stays open). Read
	// from the daemon's `/etc/containarium/otel.bearer` via
	// LoadOrCreateOTelBearer in internal/server. Phase 2.5
	// follow-up (audit C-HIGH-5).
	OTelBearer string

	// Git source provisioning. When GitSource is set, Create fetches
	// the repo into WorkspacePath inside the box (via incus exec, no
	// caller→box SSH) right after the container is up + packaged.
	// See prd/oss/create-git-source.md (Containarium-cloud).
	GitSource     string // clone URL, e.g. "https://github.com/org/repo"
	GitRef        string // SHA / branch / tag / "refs/pull/N/merge"; empty = default branch
	GitCredential string // bearer token for private repos; daemon-side only, never persisted in the box
	WorkspacePath string // where to place source; empty defaults to "/workspace"
}

// New creates a new container manager backed by a real Incus client.
func New() (*Manager, error) {
	client, err := incus.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create Incus client: %w", err)
	}

	return &Manager{incus: client, mirrors: defaultRegistryMirrors()}, nil
}

// NewWithBackend creates a new container manager backed by the given Incus
// implementation. Used by tests to inject a mock; production code uses New.
func NewWithBackend(backend incus.Backend) *Manager {
	return &Manager{incus: backend}
}

// resolveGPUDevices turns user-supplied GPU inputs ("0"/"1" or PCI address)
// into Incus GPU device configs pinned by stable PCI address. Each input is
// resolved via the backend; a GPU requested more than once (same resolved
// PCI) is rejected — attaching one physical GPU twice is always a mistake.
// See the create-time comment in Create for why we pin by PCI not DRM id.
func resolveGPUDevices(b incus.Backend, inputs []string) ([]incus.GPUDevice, error) {
	devices := make([]incus.GPUDevice, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		pci, err := b.ResolveGPUInputToPCI(in)
		if err != nil {
			return nil, err
		}
		if seen[pci] {
			return nil, fmt.Errorf("GPU %q (PCI %s) requested more than once", in, pci)
		}
		seen[pci] = true
		devices = append(devices, incus.GPUDevice{PCI: pci})
	}
	return devices, nil
}

// createJumpServerAccountFn and addAuthorizedKeyFn are the host-side
// account operations Create runs in its jump-account goroutine.
//
// vars, not direct calls, so tests can stub the host side (useradd et
// al.) and exercise the concurrency contract; production keeps the
// real functions. Same idiom as waitNetPollMin in pkg/core/incus.
var (
	createJumpServerAccountFn = CreateJumpServerAccount
	addAuthorizedKeyFn        = AddAuthorizedKey
	// ensureJumpServerAccountFn creates the account with no key material, for
	// boxes created without SSH keys (#2489).
	ensureJumpServerAccountFn = EnsureJumpServerAccount
	// userExistsFn and deleteJumpServerAccountFn let a failed create remove the
	// host account it made, and let tests keep `id` and `userdel` off the host
	// (#2850).
	userExistsFn              = userExists
	deleteJumpServerAccountFn = DeleteJumpServerAccount
)

// Create creates a new container with full setup
func (m *Manager) Create(opts CreateOptions) (*incus.ContainerInfo, error) {
	if opts.NetworkTenant != "" {
		if err := incus.ValidateNetworkTenant(opts.NetworkTenant); err != nil {
			return nil, err
		}
		if cloud := opts.Labels[incus.CloudOrgIDLabel]; cloud != "" && cloud != opts.NetworkTenant {
			return nil, fmt.Errorf("network Tenant conflicts with cloud attribution")
		}
	}
	createStart := time.Now()
	containerName := opts.Username + "-container"

	// An unknown stack is refused before anything is created (#2860):
	// installPackages silently skips an ID it cannot resolve, which would
	// leave a box with nothing installed. The RPC rejects it first; this
	// guards callers that reach the Manager directly.
	if err := stacks.GetDefault().CheckStackID(opts.Stack); err != nil {
		return nil, err
	}

	if opts.Verbose {
		fmt.Printf("Creating container: %s\n", containerName)
	}

	// Step 1: Create container
	if opts.Verbose {
		fmt.Println("  [1/6] Creating container...")
	}

	// Resolve image: OSType takes precedence over raw image string
	image := opts.Image
	if opts.OSType != pb.OSType_OS_TYPE_UNSPECIFIED {
		image = ostype.ImageForOSType(opts.OSType)
	}

	isWindows := ostype.IsWindows(opts.OSType)

	// Container or VM (#2196). Decided before anything is created so an
	// invalid pair (Windows as a container) never reaches Incus.
	instanceType, err := resolveInstanceType(opts.OSType, opts.Isolation)
	if err != nil {
		return nil, err
	}
	isVM := instanceType == incusapi.InstanceTypeVM
	if isVM && !isWindows {
		image = vmImageFor(image)
	}

	// Baked-image fast path (#1037): when an operator has baked a base image
	// for this exact (source image, podman) combination (`containarium
	// image-bake`), clone the baked image and skip the multi-minute
	// in-container package install below — the bake ran the identical
	// installPackages. Stacks are not baked, so a stack request keeps the
	// full path. No baked alias (or a lookup error) → today's path,
	// byte-identical. Baked images are container images, so a VM never
	// takes it.
	usingBakedImage := false
	if opts.Stack == "" && !isVM {
		alias := BakedImageAliasFor(image)
		if props, ok, err := m.incus.GetImageAliasProperties(alias); err == nil && ok &&
			bakedImageMatches(props, image, opts.EnablePodman) {
			if opts.Verbose {
				fmt.Printf("  Using baked base image %s (baked %s)\n", alias, props[bakedPropAt])
			}
			image = alias
			usingBakedImage = true
		}
	}

	// Labels (including OS type) ride in the create request itself via
	// ExtraConfig, rather than a separate post-create SetLabels call.
	// SetLabels is GetInstance + UpdateInstance — two more API round-trips
	// on every create for a fresh instance that has no prior labels to
	// merge against. Folding them in removes that pair entirely.
	if opts.Labels == nil {
		opts.Labels = make(map[string]string)
	}
	opts.Labels[ostype.OSTypeLabelKey] = ostype.LabelValue(opts.OSType)
	if opts.Verbose {
		fmt.Printf("  Setting %d label(s)...\n", len(opts.Labels))
	}
	labelConfig := make(map[string]string, len(opts.Labels)+1)
	if opts.NetworkTenant != "" {
		if isVM {
			return nil, fmt.Errorf("explicit network Tenant requires LXC")
		}
		// Persist the native membership with the initial instance config.
		// Generic labels are not authoritative network membership.
		labelConfig[incus.TenantLabelKey] = opts.NetworkTenant
	}
	for k, v := range opts.Labels {
		labelConfig[incus.LabelPrefix+k] = v
	}
	// Record the requested stack so list/get can report it later (#2163).
	// Create refuses an unknown stack above, so this is always a resolvable
	// ID; the check stays as defence in depth so a stack that was never
	// installed can never be recorded.
	if opts.Stack != "" && stacks.GetDefault().ValidateStackID(opts.Stack) {
		labelConfig[incus.StackKey] = opts.Stack
	}

	config := incus.ContainerConfig{
		Name:                   containerName,
		Image:                  image,
		CPU:                    opts.CPU,
		Memory:                 opts.Memory,
		EnableNesting:          opts.EnablePodman,
		EnablePodmanPrivileged: opts.EnablePodmanPrivileged,
		// Explicit membership requires a guarded, non-started instance.
		AutoStart:   opts.AutoStart && opts.NetworkTenant == "",
		Env:         otelEnvVars(opts, containerName),
		ExtraConfig: labelConfig,
	}
	// Record whether podman was requested, always, so a box created with it
	// off is distinguishable from a legacy box with no key (#2858).
	config.ExtraConfig[incus.PodmanEnabledKey] = strconv.FormatBool(opts.EnablePodman)

	// VMs: nesting and privileged podman are LXC concepts (the guest has its
	// own kernel; Docker inside it needs nothing from the host). Windows
	// additionally gets its minimum resources; Linux VMs keep the caller's
	// limits (or Incus's own 1 vCPU / 1 GiB defaults).
	if isVM {
		config.InstanceType = incusapi.InstanceTypeVM
		config.EnableNesting = false
		config.EnablePodmanPrivileged = false
	}
	if isWindows {
		if config.CPU == "" {
			config.CPU = "4"
		}
		if config.Memory == "" {
			config.Memory = "8GB"
		}
	}

	// Configure the root disk device when a size OR an explicit pool is
	// requested.
	//
	// The pool half is not symmetry for its own sake. A root disk device used
	// to be emitted only for an explicit size, so a create that named a pool
	// but no size inherited the DEFAULT PROFILE's root disk instead — the
	// daemon-wide pool. For an encrypted create that means landing outside the
	// tenant's encryptionroot, unencrypted, while reporting success, and
	// nothing about the request would look wrong (#1339).
	//
	// A create that names no pool is unaffected in either direction: same
	// condition, same pool, same device.
	diskSize := opts.Disk
	if diskSize == "" && isWindows {
		diskSize = "50GB"
	}
	if diskSize != "" || opts.StoragePool != "" {
		pool := opts.StoragePool
		if pool == "" {
			pool = m.incus.StoragePool()
		}
		config.Disk = &incus.DiskDevice{
			Path: "/",
			Pool: pool,
			// Empty stays empty — DiskDevice.ToMap omits the size key, so the
			// box gets the pool's own default rather than a size we invented.
			Size: diskSize,
		}
	}

	// Configure network interface with optional static IP
	if opts.StaticIP != "" {
		config.NIC = &incus.NICDevice{
			Name:        "eth0",
			Network:     "incusbr0",
			IPv4Address: opts.StaticIP,
		}
	}

	// Configure GPU passthrough.
	//
	// Resolve each user-supplied input ("0", "1", or a PCI address) to a
	// stable PCI address. Passing the input through as Incus' "id" field is
	// brittle: that field maps to the DRM card minor index, which can shift
	// across kernel upgrades. We hit this in production when the 6.8.0-110 →
	// 6.8.0-111 upgrade renumbered a backend host's RTX 4090 from card0 to
	// card1, breaking every container with id="0". PCI addresses are stable,
	// so we always pin by PCI. Duplicate GPUs (same resolved PCI) are
	// rejected — attaching one physical GPU twice is always a mistake.
	if len(opts.GPUs) > 0 {
		gpus, err := resolveGPUDevices(m.incus, opts.GPUs)
		if err != nil {
			return nil, fmt.Errorf("GPU passthrough setup failed: %w", err)
		}
		config.GPUs = gpus
	}

	if !isWindows && config.InstanceType != incusapi.InstanceTypeVM && toolchain.Available(m.managedToolchainRoot()) {
		config.ManagedToolchainRoot = m.managedToolchainRoot()
	}
	if err := m.timed(StageCreateInstance, func() error {
		return m.incus.CreateContainer(config)
	}); err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Guard the NIC before the first start (tenant network guard): a box
	// that cannot be guarded is deleted rather than started reachable.
	if err := m.guardNIC(containerName, opts); err != nil {
		return nil, m.cleanupGuardedCreate(containerName, err)
	}
	if opts.NetworkTenant != "" && opts.AutoStart {
		if err := m.incus.SetConfig(containerName, "boot.autostart", "true"); err != nil {
			return nil, m.cleanupGuardedCreate(containerName, fmt.Errorf("%w: guarded autostart: %v", ErrNICGuard, err))
		}
	}

	// Step 2: Start container
	if opts.Verbose {
		fmt.Println("  [2/6] Starting container...")
	}

	if err := m.timed(StageStartInstance, func() error {
		return m.incus.StartContainer(containerName)
	}); err != nil {
		// Clean up on failure
		_ = m.incus.DeleteContainer(containerName)
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Windows VM: separate provisioning flow
	if isWindows {
		return m.provisionWindowsVM(containerName, &opts)
	}

	// --- Linux container provisioning (steps 3-7) ---

	// Step 3: Create jump server account (proxy-only, no shell access).
	//
	// Runs CONCURRENTLY with the guest-side stages below (network wait,
	// package install, user seeding): it is purely host-side — useradd +
	// authorized_keys on the daemon host — and nothing in the create path
	// reads its result before the join at the end. Overlapping it with the
	// multi-second guest stages takes its ~100-300ms off the critical path.
	// The join happens before GetContainer, so the error semantics are
	// unchanged: a failed jump account still fails the create and cleans up
	// the container.
	if opts.Verbose {
		fmt.Println("  [3/7] Creating jump server account (proxy-only)...")
	}

	// Whether the account was already there decides, on a failed create,
	// whether it is ours to remove (#2850). Read before the goroutine starts so
	// the answer cannot be "yes" only because the goroutine got there first.
	accountPreexisted := userExistsFn(opts.Username)

	var jumpAccountDone chan error
	if len(opts.SSHKeys) > 0 {
		jumpAccountDone = make(chan error, 1)
		go func() {
			jumpAccountDone <- m.timed(StageJumpAccount, func() error {
				// Seed the jump-server account with the first key.
				if err := createJumpServerAccountFn(opts.Username, opts.SSHKeys[0], opts.Verbose); err != nil {
					return fmt.Errorf("failed to create jump server account: %w", err)
				}
				// Then authorize the REST of the keys host-side. CreateJumpServerAccount
				// only seeds the host /home/<user>/.ssh/authorized_keys with SSHKeys[0];
				// without this loop the remaining keys land in the CONTAINER's
				// authorized_keys (addSSHKeys, step 7) but NOT in the host jump-user
				// file that ServeAuthorizedKeys exposes and the sentinel syncs into
				// sshpiper. A client using any key other than SSHKeys[0] is then
				// rejected at the sentinel (publickey) even though the box would accept
				// it — e.g. an automation/runner key that sorts after a registered key.
				// Mirrors the seed-then-authorize-the-rest pattern in collaborator.go.
				for _, key := range opts.SSHKeys[1:] {
					if err := addAuthorizedKeyFn(opts.Username, key); err != nil {
						return fmt.Errorf("failed to authorize additional jump-server ssh key: %w", err)
					}
				}
				return nil
			})
		}()
	} else {
		// No keys is not "no account" (#2489). The host-side account is what the
		// sentinel routes SSH to; skipping it left a RUNNING box that no
		// certificate or later key push could ever reach ("host user does not
		// exist"). Create it empty; keys are added to it later.
		if opts.Verbose {
			fmt.Println("       No SSH keys provided, creating the jump server account without keys")
		}
		jumpAccountDone = make(chan error, 1)
		go func() {
			jumpAccountDone <- m.timed(StageJumpAccount, func() error {
				if err := ensureJumpServerAccountFn(opts.Username); err != nil {
					return fmt.Errorf("failed to create jump server account: %w", err)
				}
				return nil
			})
		}()
	}

	// Step 4: Wait for network
	if opts.Verbose {
		fmt.Println("  [4/7] Waiting for network...")
	}

	var ipAddr string
	if err := m.timed(StageWaitNetwork, func() error {
		var err error
		ipAddr, err = m.incus.WaitForNetwork(containerName, 30*time.Second)
		return err
	}); err != nil {
		return nil, m.abortCreate(containerName, opts.Username, jumpAccountDone, accountPreexisted,
			fmt.Errorf("failed to get container IP: %w", err))
	}

	if opts.Verbose {
		fmt.Printf("  Container IP: %s\n", ipAddr)
	}

	// Drop the dotenv copy of the monitoring env vars for nested
	// docker / docker-compose apps (which don't inherit the LXC env).
	// Best-effort: the Incus-config env (config.Env) is already set for
	// native LXC apps, so a file-write failure shouldn't fail create.
	// See #370.
	if len(config.Env) > 0 {
		if err := m.WriteOTelEnvFile(containerName, config.Env); err != nil {
			log.Printf("[otel] %s: failed to write env file: %v (native-LXC env still stamped)", containerName, err)
		}
	}

	// Signal provisioning state — container is running but still installing packages
	if opts.OnProvisioning != nil {
		opts.OnProvisioning()
	}

	// Step 5: Install packages
	if opts.Verbose {
		if opts.Stack != "" {
			fmt.Printf("  [5/7] Installing Podman, SSH, tools, and %s stack...\n", opts.Stack)
		} else {
			fmt.Println("  [5/7] Installing Podman, SSH, and tools...")
		}
	}

	family := ostype.FamilyForOSType(opts.OSType)
	if usingBakedImage {
		// Baked base image (#1037): the bake already ran this exact
		// installPackages (same image, same podman setting, no stack) into
		// the image this box was cloned from — the multi-minute in-container
		// package install is the whole cost the bake exists to skip.
		if opts.Verbose {
			fmt.Println("  [5/7] Package install pre-baked into the base image — skipping")
		}
	} else if err := m.timed(StageInstallPackages, func() error {
		return m.installPackages(containerName, opts.EnablePodman, opts.Stack, opts.StackParameters, opts.Username, family)
	}); err != nil {
		return nil, m.abortCreate(containerName, opts.Username, jumpAccountDone, accountPreexisted,
			fmt.Errorf("failed to install packages: %w", err))
	}

	if config.ManagedToolchainRoot != "" {
		if err := m.incus.Exec(containerName, []string{"sh", "-c", toolchain.SetupScript()}); err != nil {
			// Optional for box creation (NONE/FULL_SUDO must remain usable).
			// BUILD_TOOLS repeats this check and refuses a broken/conflicting
			// tool setup before starting any run.
			log.Printf("[toolchains] %s: tools unavailable: %v", containerName, err)
		}
	}

	// Bring the box under the sshd policy at birth (#2424), on the baked
	// path too (it skips installPackages, but the baked image carries the
	// drop-in). Without the marker the posture reconciler would read a
	// tenant edit made before its first pass as "never applied" and
	// backfill it silently instead of raising a finding.
	m.markSSHDPolicy(containerName)

	// Make podman workloads reboot-durable (#387). Runs on BOTH the baked
	// and full paths: the rootful half is already enabled in a baked image
	// (idempotent re-enable, harmless), but the rootless half is per-user
	// (linger + user service) and can only run once the tenant is known.
	if opts.EnablePodman {
		m.enablePodmanRestartDurability(containerName, opts.Username)
	}

	// Step 6: Create user
	if opts.Verbose {
		fmt.Printf("  [6/7] Creating user: %s...\n", opts.Username)
	}

	if err := m.timed(StageCreateUser, func() error {
		return m.createUser(containerName, opts.Username, family)
	}); err != nil {
		return nil, m.abortCreate(containerName, opts.Username, jumpAccountDone, accountPreexisted,
			fmt.Errorf("failed to create user: %w", err))
	}

	// Step 7: Add SSH keys (including jump server's key for ProxyJump support)
	allKeys := make([]string, 0, len(opts.SSHKeys)+1)

	// Add jump server's SSH key automatically for ProxyJump support
	jumpServerKey, err := getJumpServerSSHKey()
	if err == nil && jumpServerKey != "" {
		allKeys = append(allKeys, jumpServerKey)
		if opts.Verbose {
			fmt.Println("  [7/7] Adding SSH keys (including jump server key for ProxyJump)...")
		}
	} else {
		if opts.Verbose {
			fmt.Println("  [7/7] Adding SSH keys...")
		}
	}

	// Add user-provided keys
	allKeys = append(allKeys, opts.SSHKeys...)

	if len(allKeys) > 0 {
		if opts.Verbose {
			fmt.Printf("       Adding %d SSH key(s)...\n", len(allKeys))
		}

		if err := m.timed(StageAddSSHKeys, func() error {
			return m.addSSHKeys(containerName, opts.Username, allKeys)
		}); err != nil {
			return nil, m.abortCreate(containerName, opts.Username, jumpAccountDone, accountPreexisted,
				fmt.Errorf("failed to add SSH keys: %w", err))
		}
	} else {
		if opts.Verbose {
			fmt.Println("       No SSH keys to add, skipping...")
		}
	}

	// Step 8: Provision git source into the workspace (optional).
	// The daemon fetches the repo via incus exec — no caller→box SSH.
	// A failed fetch fails the create: a box without its source is
	// useless, and a half-populated workspace is worse than none.
	if opts.GitSource != "" {
		if opts.Verbose {
			fmt.Printf("  [8/8] Fetching git source %s into %s...\n", opts.GitSource, gitWorkspacePath(opts.WorkspacePath))
		}
		if err := m.timed(StageGitSource, func() error {
			return m.provisionGitSource(containerName, opts)
		}); err != nil {
			return nil, m.abortCreate(containerName, opts.Username, jumpAccountDone, accountPreexisted,
				fmt.Errorf("failed to provision git source: %w", err))
		}
	}

	// Join the jump-account goroutine (step 3). Every failure return above
	// joins it too, through abortCreate (#2850): it used to be skipped, which
	// left the host account behind, and left the goroutine free to create it
	// after the rollback had already run.
	if jumpAccountDone != nil {
		if err := <-jumpAccountDone; err != nil {
			return nil, m.abortCreate(containerName, opts.Username, nil, accountPreexisted, err)
		}
	}

	// Get container info
	var info *incus.ContainerInfo
	if err := m.timed(StageGetInfo, func() error {
		var err error
		info, err = m.incus.GetContainer(containerName)
		return err
	}); err != nil {
		return nil, fmt.Errorf("failed to get container info: %w", err)
	}

	if m.observeStage != nil {
		m.observeStage(StageTotal, time.Since(createStart))
	}
	return info, nil
}

// generatePassword generates a random password of the given byte length (hex-encoded).
func generatePassword(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// provisionWindowsVM handles the Windows-specific provisioning after the VM
// has been created, started, and labelled.
func (m *Manager) provisionWindowsVM(vmName string, opts *CreateOptions) (*incus.ContainerInfo, error) {
	if opts.Verbose {
		fmt.Println("  [3/4] Waiting for Windows VM network (this may take 1-2 minutes)...")
	}

	// Windows VMs take much longer to boot than Linux containers
	ipAddr, err := m.incus.WaitForNetwork(vmName, 120*time.Second)
	if err != nil {
		_ = m.cleanup(vmName)
		return nil, fmt.Errorf("failed to get VM IP: %w", err)
	}

	if opts.Verbose {
		fmt.Printf("  VM IP: %s\n", ipAddr)
	}

	// Signal provisioning state
	if opts.OnProvisioning != nil {
		opts.OnProvisioning()
	}

	// Generate and set Administrator password
	if opts.Verbose {
		fmt.Println("  [4/4] Setting Administrator password and verifying RDP...")
	}

	password, err := generatePassword(16)
	if err != nil {
		_ = m.cleanup(vmName)
		return nil, fmt.Errorf("failed to generate password: %w", err)
	}

	// Set Administrator password via PowerShell
	psCmd := fmt.Sprintf(
		"Set-LocalUser -Name Administrator -Password (ConvertTo-SecureString '%s' -AsPlainText -Force)",
		password,
	)
	if err := m.incus.Exec(vmName, []string{"powershell", "-Command", psCmd}); err != nil {
		_ = m.cleanup(vmName)
		return nil, fmt.Errorf("failed to set Administrator password: %w", err)
	}

	// Verify RDP is listening on port 3389
	rdpCheck := "Test-NetConnection -ComputerName localhost -Port 3389 -InformationLevel Quiet"
	if err := m.incus.Exec(vmName, []string{"powershell", "-Command", rdpCheck}); err != nil {
		if opts.Verbose {
			fmt.Println("  Warning: RDP port 3389 check failed — RDP may not be enabled in the golden image")
		}
	}

	// Store RDP password as a label on the VM for the server to retrieve
	opts.RDPPassword = password
	if err := m.incus.SetLabels(vmName, map[string]string{
		"rdp-password": password,
	}); err != nil {
		log.Printf("Warning: failed to store RDP password label: %v", err)
	}

	info, err := m.incus.GetContainer(vmName)
	if err != nil {
		return nil, fmt.Errorf("failed to get VM info: %w", err)
	}

	if opts.Verbose {
		fmt.Printf("  Windows VM ready: RDP at %s:3389 (user: Administrator)\n", ipAddr)
	}

	return info, nil
}

// installPackages installs required packages in the container
// stackEnvPrefix builds a shell fragment that exports stack parameters as
// environment variables (CONTAINARIUM_STACK_<name>) for the install script.
// Returns empty string if params is empty. Values are single-quoted with
// embedded single-quotes escaped.
func stackEnvPrefix(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range params {
		// Single-quote escape: close, escape, reopen. 'foo'\''bar' => foo'bar
		escaped := strings.ReplaceAll(v, "'", `'\''`)
		b.WriteString("export CONTAINARIUM_STACK_")
		b.WriteString(k)
		b.WriteString("='")
		b.WriteString(escaped)
		b.WriteString("'; ")
	}
	return b.String()
}

// cloudInitPresent reports whether containerName's image has cloud-init on
// its PATH (#1530). A probe failure (transport error) or a non-zero exit
// (genuinely absent, or a shell that doesn't support `command -v` — neither
// worth distinguishing here) both count as "not present": the wait this
// guards is a courtesy, not a correctness requirement, so failing safe here
// only costs a skipped wait, never a false "it's fine to proceed" on a boot
// that's actually still settling.
func (m *Manager) cloudInitPresent(containerName string) bool {
	_, _, exitCode, err := m.incus.ExecWithExitCode(containerName, []string{"sh", "-c", "command -v cloud-init"})
	return err == nil && exitCode == 0
}

func (m *Manager) installPackages(containerName string, enablePodman bool, stackID string, stackParams map[string]string, username string, family ostype.OSFamily) error {
	pkgMgr := ospkg.ForFamily(family)
	familyStr := string(family)

	// Wait a bit for cloud-init to finish, but only when the image actually
	// ships it (#1530): a benchmark investigation found images:ubuntu/24.04
	// — this daemon's default create base — has no cloud-init at all, so
	// this wait was pure dead time on every stackless create against it
	// (~5s of the ~90s a create took at 200m CPU).
	if m.cloudInitPresent(containerName) {
		wait := m.cloudInitWait
		if wait == 0 {
			wait = defaultCloudInitWait
		}
		time.Sleep(wait)
	}

	// Update package lists
	if err := m.incus.Exec(containerName, pkgMgr.UpdateCmd()); err != nil {
		return fmt.Errorf("package repo update failed: %w", err)
	}

	// Build package list from OS-specific base packages
	packages := pkgMgr.BasePackages()

	// Add Podman
	if enablePodman {
		if pkgMgr.PodmanAvailableInBaseRepos() {
			// RHEL/Rocky: podman is in base repos, just add to package list
			packages = append(packages, "podman")
		} else {
			// Debian/Ubuntu: need Kubic repo for newer Podman
			prereqCmd := pkgMgr.InstallCmd([]string{"curl", "gpg"})
			if err := m.incus.Exec(containerName, prereqCmd); err != nil {
				return fmt.Errorf("failed to install prerequisites: %w", err)
			}

			repoScript := pkgMgr.PodmanRepoScript()
			if repoScript != "" {
				if err := m.incus.Exec(containerName, []string{"/bin/bash", "-c", repoScript}); err != nil {
					log.Printf("Warning: failed to add Podman repository, using distro's podman: %v", err)
				}
			}
			packages = append(packages, "podman")
		}
	}

	// Collect base script packages and run their pre-install commands
	stackMgr := stacks.GetDefault()
	baseScripts := stackMgr.GetAllBaseScripts()
	for _, bs := range baseScripts {
		for _, cmd := range bs.GetPreInstallForFamily(familyStr) {
			cmd = strings.ReplaceAll(cmd, "{{USERNAME}}", username)
			_ = m.incus.Exec(containerName, []string{"bash", "-c", cmd})
		}
		packages = append(packages, bs.GetPackagesForFamily(familyStr)...)
	}

	// Add stack-specific packages and run pre-install commands
	envPrefix := stackEnvPrefix(stackParams)
	if stackID != "" {
		stack, err := stackMgr.GetStack(stackID)
		if err == nil {
			// Run pre-install commands as root (e.g., adding repos). Stack
			// parameters are exported as CONTAINARIUM_STACK_<name> env vars.
			for _, cmd := range stack.GetPreInstallForFamily(familyStr) {
				_ = m.incus.Exec(containerName, []string{"bash", "-c", envPrefix + cmd})
			}
			packages = append(packages, stack.GetPackagesForFamily(familyStr)...)
		}
	}

	// Install packages
	if err := m.incus.Exec(containerName, pkgMgr.InstallCmd(packages)); err != nil {
		return fmt.Errorf("package install failed: %w", err)
	}

	// mosh + tmux-on-login for phone clients (#2121). User-independent, so
	// it stays inside installPackages and lands in baked images too.
	m.installLoginExtras(containerName, pkgMgr)

	// Enable services and install podman-compose
	if enablePodman {
		if err := m.incus.Exec(containerName, []string{"systemctl", "enable", "podman"}); err != nil {
			return fmt.Errorf("failed to enable podman: %w", err)
		}
		if err := m.incus.Exec(containerName, []string{"systemctl", "start", "podman"}); err != nil {
			return fmt.Errorf("failed to start podman: %w", err)
		}

		// #908: point the box's podman at the operator-configured registry
		// mirror(s) so multi-GB image pulls come from the LAN, not the WAN. A
		// non-destructive registries.conf.d drop-in; no-op when no mirror is
		// configured. Best-effort: a mirror-config failure must not fail box
		// provisioning — podman still pulls from upstream (today's behavior).
		if err := m.writeRegistryMirrors(containerName); err != nil {
			log.Printf("Warning: registry-mirror config on %s: %v", containerName, err)
		}

		// Install podman-compose via pip
		if err := m.incus.Exec(containerName, pkgMgr.PipInstallCmd()); err != nil {
			log.Printf("Warning: failed to install pip: %v", err)
		} else {
			if err := m.incus.Exec(containerName, []string{"pip3", "install", "--break-system-packages", "podman-compose"}); err != nil {
				log.Printf("Warning: failed to install podman-compose via pip: %v", err)
			}
		}

		// Per-user podman reboot durability runs from Create (not here):
		// installPackages must stay user-independent so BakeBaseImage
		// (#1037) can run it into a shared base image. See issue #387.
	}

	// Key-only SSH inside the box (#2424). A box sshd otherwise inherits
	// the image default (PasswordAuthentication yes), and the owner's
	// NOPASSWD sudo is one `passwd` away from making that reachable. The
	// managed drop-in sorts before every distro/cloud-init drop-in, so it
	// wins under sshd's first-match rule. It is user-independent, so it
