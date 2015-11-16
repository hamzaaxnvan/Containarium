package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	sandboxclient "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned"

	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Config is the K8s backend's wiring, supplied at daemon start when
// CONTAINARIUM_RUNTIME=k8s selects this backend. See
// docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md.
type Config struct {
	// Kubeconfig is the path to a kubeconfig file. Empty means: try in-cluster
	// config, then the ambient loading rules ($KUBECONFIG / ~/.kube/config).
	Kubeconfig string

	// GatewayNamespace is where the sshpiper Deployment + its LB Service live.
	GatewayNamespace string

	// GatewayHost / GatewaySSHPort is the public SSH endpoint agents connect to
	// (the sshpiper LB), surfaced in BoxEndpoint so the server builds the
	// connect target without inferring it.
	GatewayHost    string
	GatewaySSHPort int

	// GatewayService is the in-cluster sshpiper Service name in
	// GatewayNamespace, used to resolve the node's SSH ingress (NodePort)
	// advertised to the sentinel. Defaults to "sshpiper".
	GatewayService string

	// GatewayAdvertisePort overrides the resolved ingress port advertised
	// to the sentinel (0 = resolve from GatewayService).
	GatewayAdvertisePort int

	// TenantNamespacePrefix prefixes the per-tenant namespace
	// (e.g. "tenant-" → "tenant-<tenant>"). Defaults to "tenant-".
	TenantNamespacePrefix string

	// BoxImage is the agent-box image (sshd + agent-box) the box runs.
	BoxImage string

	// BoxMode is passed to the box as the AGENTBOX_MODE env var: "" or "mcp"
	// keeps the forced-command MCP session (default), "shell" gives the box an
	// interactive login shell (developer-box). Empty leaves the image default.
	BoxMode string

	// StorageClass backs the per-box data PVC.
	StorageClass string

	// SharedVolumeStorageClass is an operator-selected RWX-capable class for
	// VolumeService PVCs. Empty disables shared volumes on this backend.
	SharedVolumeStorageClass string

	// RuntimeClass names the Kubernetes RuntimeClass every box pod is scheduled
	// under. Empty (the default) leaves RuntimeClassName unset, so pods run on
	// the cluster's default container runtime — `runc`, sharing the host
	// kernel. Set it to "runsc" on a node pool that has gVisor installed to put
	// boxes behind a userspace kernel instead.
	//
	// This is daemon-wide, not per box: the runtime is a property of the node
	// pool the daemon schedules onto. Per-box runtime selection is a separate,
	// unshipped design (a typed runtime_class on the container spec) gated on
	// the gVisor feasibility benchmark.
	RuntimeClass string

	// DefaultMemoryRequest / DefaultMemoryLimit override the built-in per-box
	// memory floor applied when a box's spec carries no valid memory quantity.
	// The request (scheduler reservation) is kept below the limit (hard cap) so
	// idle boxes pack densely on the shared host kernel while the limit stops any
	// one box ballooning and pressuring its neighbors (noisy-neighbor). Empty →
	// the built-in defaults (defaultMemoryRequest / defaultMemoryLimit); a value
	// that isn't a valid K8s quantity also falls back to the built-in default.
	// These are cluster-size specific — tune to the node pool (e.g. "512Mi",
	// "2Gi"). Ignored when DisableDefaultMemoryFloor is set.
	DefaultMemoryRequest string
	DefaultMemoryLimit   string

	// DisableDefaultMemoryFloor turns the automatic per-box memory floor off
	// entirely: boxes with no explicit memory run unconstrained (the pre-floor
	// behavior). An escape hatch for dedicated / single-tenant nodes; on shared
	// nodes it lets a box balloon, so it is not recommended there.
	DisableDefaultMemoryFloor bool

	// Gateway upstream credential (sshpiper → box hop). sshpiper terminates the
	// client connection and opens a NEW connection to the box as user `agent`,
	// authenticating with its own key. So:
	//   - GatewayUpstreamPublicKey is authorized ON each box (sshpiper logs in).
	//   - GatewayUpstreamKeySecret is the Secret in GatewayNamespace holding the
	//     matching private key, referenced by each Pipe's spec.to.private_key_secret.
	// The agent's own keys (spec.SSHKeys) authenticate client→gateway via the
	// Pipe's spec.from.authorized_keys_data. When these are unset, boxes
	// authorize the agent keys directly (no-gateway / direct-SSH mode).
	GatewayUpstreamPublicKey string
	GatewayUpstreamKeySecret string

	// InsecureIgnoreHostKey keeps the pre-pinning behavior (the Pipe sets
	// ignore_hostkey instead of pinning the box's host key via known_hosts_data).
	// Default false = pin. An escape hatch, not the recommended posture.
	InsecureIgnoreHostKey bool
}

// Backend implements box.BoxBackend on Kubernetes, on top of the
// kubernetes-sigs/agent-sandbox Sandbox CRD: the daemon owns the per-tenant
// namespace, Secrets, NetworkPolicy, data PVC, and gateway Pipe, and declares
// one Sandbox CR per box — the agent-sandbox controller owns the pod and the
// headless Service under it. The controller must be installed in the cluster
// (its v0.5.1 release asset `manifest.yaml`).
//
// Capability note: it deliberately does NOT implement box.ExecCapable — the
// K8s agent-box is ForceCommand-pinned and provisioning is image-baked, so
// there is no in-box exec seam. box.MetricsCapable / box.GPUCapable are
// deferred.
type Backend struct {
	cfg       Config
	clientset kubernetes.Interface
	sandboxes sandboxclient.Interface
	dyn       dynamic.Interface // for the sshpiper Pipe CRD; nil disables gateway routing
	router    gatewayRouter     // SSH-gateway routing seam; sshpiperRouter today
}

var _ box.BoxBackend = (*Backend)(nil)

// New constructs a K8s backend: it builds a client-go clientset, the
// agent-sandbox typed clientset, and a dynamic client (in-cluster, an explicit
// kubeconfig, or the ambient rules) and applies config defaults.
func New(cfg Config) (*Backend, error) {
	restCfg, err := buildRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("k8s: build rest config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: build clientset: %w", err)
	}
	sc, err := sandboxclient.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: build agent-sandbox clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: build dynamic client: %w", err)
	}
	return newBackend(cs, sc, dyn, cfg), nil
}

// NewWithClientset builds a Backend over injected clientsets, with gateway
// (Pipe) routing disabled (no dynamic client). Used by lifecycle unit tests.
func NewWithClientset(cs kubernetes.Interface, sc sandboxclient.Interface, cfg Config) *Backend {
	return newBackend(cs, sc, nil, cfg)
}

// NewWithClients builds a Backend over injected clientsets + dynamic client
// (fakes for unit tests, a kind cluster for e2e), exercising gateway routing.
func NewWithClients(cs kubernetes.Interface, sc sandboxclient.Interface, dyn dynamic.Interface, cfg Config) *Backend {
	return newBackend(cs, sc, dyn, cfg)
}

func newBackend(cs kubernetes.Interface, sc sandboxclient.Interface, dyn dynamic.Interface, cfg Config) *Backend {
	if cfg.TenantNamespacePrefix == "" {
		cfg.TenantNamespacePrefix = "tenant-"
	}
	if cfg.GatewaySSHPort == 0 {
		cfg.GatewaySSHPort = 22
	}
	if cfg.GatewayService == "" {
		cfg.GatewayService = "sshpiper"
	}
	// Resolve the default memory floor once: built-in defaults when unset, the
	// built-in default when an operator value isn't a valid K8s quantity (a typo
	// degrades to the safe floor rather than silently leaving boxes unconstrained),
	// or cleared entirely when explicitly disabled. The object builders then
	// consume the resolved, pre-validated strings via b.memDefaults().
	if cfg.DisableDefaultMemoryFloor {
		cfg.DefaultMemoryRequest, cfg.DefaultMemoryLimit = "", ""
	} else {
		cfg.DefaultMemoryRequest = resolveQuantity(cfg.DefaultMemoryRequest, defaultMemoryRequest)
		cfg.DefaultMemoryLimit = resolveQuantity(cfg.DefaultMemoryLimit, defaultMemoryLimit)
	}
	b := &Backend{cfg: cfg, clientset: cs, sandboxes: sc, dyn: dyn}
	b.router = &sshpiperRouter{b: b}
	return b
}

// resolveQuantity returns v when it is a valid K8s resource quantity, else the
// fallback. Used to validate operator-supplied Config defaults at construction
// so the object builders can MustParse them safely.
func resolveQuantity(v, fallback string) string {
	if v == "" {
		return fallback
	}
	if _, err := resource.ParseQuantity(v); err != nil {
		return fallback
	}
	return v
}

// memDefaults returns the resolved cluster-wide default memory floor consumed by
// the object builders. An empty limit (when DisableDefaultMemoryFloor is set)
// disables the floor.
func (b *Backend) memDefaults() memDefaults {
	return memDefaults{request: b.cfg.DefaultMemoryRequest, limit: b.cfg.DefaultMemoryLimit}
}

// isOCIImageReference reports whether ref is shaped like an OCI image
// reference, i.e. whether kubelet can parse it at all. Create handles the
// empty case separately, so this is only asked about a non-empty ref.
//
// It exists because the create path's image default is an Incus remote alias
// ("images:ubuntu/24.04"), which is a valid image on the LXC backend and
// garbage here (#1524). The two grammars overlap almost everywhere - "x",
// "nginx:1.25" and "ubuntu:24.04" are legal in both - and separate on exactly
// one point: an OCI reference's registry component (everything before the
// first "/") may carry a ":" only as a port, while an Incus alias puts the
// remote name there. That single rule is all this checks. It is a shape test
// against the alias grammar, not validation of the whole reference grammar,
// so a slash-free alias ("ubuntu:24.04") still passes - that one is a legal
// OCI reference, and fails at pull time rather than at parse time.
func isOCIImageReference(ref string) bool {
	registry, _, ok := strings.Cut(ref, "/")
	if !ok {
		// No registry component: "name[:tag][@digest]" is shape-valid either way.
		return true
	}
	// An IPv6 registry host is bracketed ("[::1]:5000"), so the port's colon is
	// the one after the closing bracket, not the ones inside the address.
	if i := strings.LastIndex(registry, "]"); i >= 0 {
		registry = registry[i+1:]
	}
	_, port, ok := strings.Cut(registry, ":")
	if !ok {
		return true
	}
	if port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
