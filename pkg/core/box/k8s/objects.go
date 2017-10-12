package k8s

import (
	"fmt"
	"github.com/footprintai/containarium/pkg/core/box/k8s/boxmeta"
	"log"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/footprintai/containarium/pkg/core/box"
)

// Object naming + labels. One box per tenant namespace: the agent-sandbox
// Sandbox CR is always "box"; its controller creates the pod (also "box") and
// a headless Service (also "box") that gives the pod stable in-cluster DNS.
const (
	sandboxName = "box"
	sshPortName = "ssh"

	// pvcName is the PersistentVolumeClaim name inside the tenant namespace.
	// It holds the box's persistent data, mounted at dataMount below. Owned by
	// the daemon, NOT declared via the Sandbox's volumeClaimTemplates:
	// template-derived PVCs are owner-referenced to the Sandbox and
	// garbage-collected with it, which would break delete-retains-data.
	// Created before the Sandbox, mounted as a plain persistentVolumeClaim
	// volume, retained on Delete; removed only by Purge.
	pvcName    = "data"
	dataVolume = "data"
	// dataMount is a subdirectory of the home directory, NOT the home
	// directory itself (#974). Mounting the PVC directly at /home/agent
	// replaces the image's real home with the provisioner's fresh volume
	// root — typically 0777 root:root (kind/local-path) or 0755 root:root
	// (a typical CSI ext4 root) — either of which either fails dropbear's
	// strict-modes home-directory check (group/world-writable) or leaves
	// uid-1000 `agent` unable to write its own home at all. Mounting one
	// level down keeps /home/agent itself exactly as the image built it
	// (owned by `agent`, dropbear-compatible perms, authorized_keys layered
	// in via a separate Secret mount) while still giving the box a
	// persistent, per-tenant working directory. Trade-off: only this
	// subdirectory survives box recreation now, not the whole home — see
	// the CSI persistent storage section in
	// docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md.
	dataMount   = "/home/agent/workspace"
	defaultDisk = "10Gi"

	// Default per-box memory bounds, applied when a box's spec carries no valid
	// memory quantity. The request (what the scheduler reserves) is deliberately
	// kept below the limit (the hard cap) so a mostly-idle box packs densely on
	// the shared host kernel — preserving the memory density LXC gave us — while
	// the limit still stops any single box from ballooning and pressuring its
	// neighbors (noisy-neighbor). GPU boxes are exempt: they're sized explicitly
	// and a small cap would OOM the workload. Override per box via
	// Resources.Memory (CLI --memory / Resize); a future Config knob can retune
	// the cluster-wide floor. CPU is intentionally left undefaulted — it's
	// compressible (a limit throttles rather than kills), so an implicit CPU cap
	// would surprise more than it protects.
	defaultMemoryRequest = "256Mi"
	defaultMemoryLimit   = "1Gi"
	// sshPort is the box's in-pod SSH port. 2222 (unprivileged) so the box
	// runs fully non-root with no added capabilities — the agent connects to
	// the gateway on :22; this is the internal sshpiper→pod hop.
	sshPort = 2222
	// boxSSHUser is the fixed login user inside the box. The gateway connects
	// upstream as this user (Pipe spec.to.username); tenant identity is
	// enforced at the gateway, not by per-tenant box users.
	boxSSHUser = "agent"

	managedByLabel       = boxmeta.ManagedByLabel
	managedByValue       = boxmeta.ManagedByValue
	tenantLabel          = boxmeta.TenantLabel
	metaAnnotationPrefix = "containarium.dev/meta."
	gpuCountAnnotation   = "containarium.dev/gpu-count"

	// nvidiaGPUResource is the K8s extended-resource name for NVIDIA GPUs.
	// A non-zero limit causes the cluster autoscaler to scale up a GPU node pool.
	nvidiaGPUResource = corev1.ResourceName("nvidia.com/gpu")

	// boxContainerName is the box container in the pod.
	boxContainerName = boxmeta.BoxContainerName

	authorizedKeysKey = "authorized_keys"
	// authorizedKeysMount is where the box image (dropbear entrypoint) reads
	// authorized_keys; the box's Secret is mounted here.
	authorizedKeysMount  = "/etc/agent-box"
	authorizedKeysVolume = "authorized-keys"

	// Per-box stable host key (so the gateway can pin it). The entrypoint reads
	// the private key here; the daemon stores it (+ the public half) in the
	// host-key Secret.
	hostKeyField       = "host_key"     // ed25519 private (OpenSSH PEM)
	hostKeyPubField    = "host_key.pub" // ed25519 public (authorized-key)
	hostKeyRSAField    = "host_key_rsa" // RSA private — dropbear needs an RSA host key (rsa-sha2)
	hostKeyRSAPubField = "host_key_rsa.pub"
	hostKeyMount       = "/etc/agent-box-hostkey"
	hostKeyVolume      = "host-key"
)

func hostKeySecretName(tenant string) string { return tenant + "-host-key" }

// pvcObject builds the PersistentVolumeClaim for the box's data volume.
// storageClass "" disables PVC provisioning (caller must not call this).
// disk is the requested size (e.g. "20Gi"); defaults to defaultDisk when empty
// or unparseable (see parseDiskQuantity).
func pvcObject(ns, tenant, storageClass, disk string) *corev1.PersistentVolumeClaim {
	quantity := parseDiskQuantity(disk)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: ns,
			Labels:    boxLabels(tenant),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: quantity,
				},
			},
		},
	}
	// A non-empty StorageClass is set explicitly. Empty string is not stored
	// (nil pointer) so the cluster's default StorageClass takes over — but in
	// practice callers with StorageClass=="" skip PVC creation entirely (see
	// Config.StorageClass semantics), so this branch is defensive-only.
