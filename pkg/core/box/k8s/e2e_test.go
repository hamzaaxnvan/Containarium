//go:build k8s

package k8s

import (
	"context"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	sandboxclient "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned"

	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestE2E_BoxLifecycle drives the reconciler against a REAL apiserver (a kind
// cluster in CI). It is gated on CONTAINARIUM_K8S_E2E so it never runs in the
// plain unit suite — only scripts/k8s-e2e.sh (and the k8s-e2e workflow) set it,
// with KUBECONFIG pointing at the cluster.
//
//	CONTAINARIUM_K8S_E2E=1 KUBECONFIG=... go test -tags k8s -run TestE2E ./pkg/core/box/k8s/
func TestE2E_BoxLifecycle(t *testing.T) {
	if os.Getenv("CONTAINARIUM_K8S_E2E") == "" {
		t.Skip("set CONTAINARIUM_K8S_E2E=1 (and KUBECONFIG) to run the kind e2e")
	}

	b, err := New(Config{
		Kubeconfig:  os.Getenv("KUBECONFIG"),
		BoxImage:    "registry.k8s.io/pause:3.9",
		GatewayHost: "gateway.example.com",
	})
	if err != nil {
		t.Fatalf("New (is the cluster reachable?): %v", err)
	}

	ctx := context.Background()
	ref := box.BoxRef{Tenant: "e2e"}
	t.Cleanup(func() { _ = b.Delete(context.Background(), ref, true) })

	// Create reconciles namespace + Secrets + NetworkPolicy + the Sandbox CR;
	// the agent-sandbox controller creates the pod and headless Service.
	if _, err := b.Create(ctx, box.BoxSpec{
		Ref:       ref,
		Image:     "registry.k8s.io/pause:3.9",
		SSHKeys:   []string{"ssh-ed25519 AAAAExampleKeyForE2E test@e2e"},
		AutoStart: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Poll until the pod is Ready (the apiserver + kubelet actually schedule it).
	st := waitForState(t, b, ref, pb.ContainerState_CONTAINER_STATE_RUNNING, 3*time.Minute)
	if st.IPAddress == "" {
		t.Errorf("running box has no pod IP")
	}

	// Stop suspends the Sandbox (controller deletes the pod) → STOPPED.
	if err := b.Stop(ctx, ref, false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForState(t, b, ref, pb.ContainerState_CONTAINER_STATE_STOPPED, time.Minute)

	// Start resumes the Sandbox (controller recreates the pod) → RUNNING.
	if err := b.Start(ctx, ref); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, b, ref, pb.ContainerState_CONTAINER_STATE_RUNNING, 3*time.Minute)

	// SetAuthorizedKeys upserts the Secret without error.
	if err := b.SetAuthorizedKeys(ctx, ref, []string{"ssh-ed25519 BBBBRotatedKey test@e2e"}); err != nil {
		t.Fatalf("SetAuthorizedKeys: %v", err)
	}

	// Delete removes the namespace; Get eventually reports absent.
	if err := b.Delete(ctx, ref, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		got, gerr := b.Get(ctx, ref)
		if gerr == nil && got == nil {
			return // gone
		}
		time.Sleep(3 * time.Second)
	}
	t.Error("box still present 2m after Delete")
}

func waitForState(t *testing.T, b *Backend, ref box.BoxRef, want pb.ContainerState, timeout time.Duration) *box.BoxStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *box.BoxStatus
	for time.Now().Before(deadline) {
		st, err := b.Get(context.Background(), ref)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		last = st
		if st != nil && st.State == want {
			return st
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("box did not reach %v within %s (last: %+v)", want, timeout, last)
	return nil
}

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// TestE2E_GatewayPipe installs the sshpiper Pipe CRD into the kind cluster
// (just the CRD, not sshpiper itself), then asserts the reconciler programs and
// removes the Pipe against the real apiserver — validating the GVR + spec shape
// end to end. A full data-plane e2e (deploy sshpiper, SSH through the gateway)
// needs the real agent-box image and is a follow-up.
func TestE2E_GatewayPipe(t *testing.T) {
	if os.Getenv("CONTAINARIUM_K8S_E2E") == "" {
		t.Skip("set CONTAINARIUM_K8S_E2E=1 (and KUBECONFIG) to run the kind e2e")
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("rest config: %v", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	ctx := context.Background()

	installPipeCRD(t, dyn)

	const gwNS = "sshpiper"
	if _, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: gwNS}}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gateway namespace: %v", err)
	}

	b, err := New(Config{
		Kubeconfig:       kubeconfig,
		BoxImage:         "registry.k8s.io/pause:3.9",
		GatewayNamespace: gwNS,
		GatewayHost:      "gateway.example.com",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref := box.BoxRef{Tenant: "gw-e2e"}
	t.Cleanup(func() { _ = b.Delete(context.Background(), ref, true) })

	if _, err := b.Create(ctx, box.BoxSpec{
		Ref:       ref,
		Image:     "registry.k8s.io/pause:3.9",
		SSHKeys:   []string{"ssh-ed25519 AAAAExampleKey test@gw-e2e"},
		AutoStart: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The reconciler should have created a Pipe in the gateway namespace.
	p, err := dyn.Resource(pipeGVR).Namespace(gwNS).Get(ctx, pipeName("gw-e2e"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pipe: %v", err)
	}
	host, _, _ := unstructured.NestedString(p.Object, "spec", "to", "host")
	if host != "box.tenant-gw-e2e.svc.cluster.local:2222" {
		t.Errorf("pipe to.host = %q", host)
	}

	// Delete removes the Pipe (it lives in the gateway ns, outside the cascade).
	if err := b.Delete(ctx, ref, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := dyn.Resource(pipeGVR).Namespace(gwNS).Get(ctx, pipeName("gw-e2e"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("pipe still present after Delete (err=%v)", err)
	}
}

// TestE2E_CSIStorage drives the PVC lifecycle against a real apiserver using
// kind's default "standard" StorageClass (local-path-provisioner, pre-installed
// by kind). It verifies Create provisions a PVC + wires the data volume into the
// StatefulSet, Delete retains the namespace + PVC while removing compute objects,
// and Purge removes both.
//
// The PVC stays in Pending until a pod is scheduled (local-path binds on pod
// assignment); we assert the object exists, not that it is Bound — the mount
// wiring is what matters for storage correctness.
//
// It also guards against #974 regressing: the data volume must land at
// dataMount (a subdirectory of the home directory), never directly at
// /home/agent — mounting the PVC over the home directory itself replaces the
// box image's real home (dropbear-strict-modes-compatible ownership/perms)
// with the provisioner's fresh volume root, which dropbear's strict `modes`
// check rejects (kind/local-path: 0777 root:root) or which locks uid-1000
// `agent` out of its own home entirely (a typical CSI ext4 root: 0755
// root:root) — either way every SSH login fails. This test uses the
// shell-less `pause` image, so it cannot drive an actual SSH login or stat
// the mounted directory's live permissions against a real apiserver; that
// finer-grained check (mount path is a home subdirectory, authorized_keys
// mount unaffected) is covered by the unit-level
// TestDataMountDoesNotOverrideHomeDirectory in k8s_test.go, which runs
// without a cluster.
func TestE2E_CSIStorage(t *testing.T) {
	if os.Getenv("CONTAINARIUM_K8S_E2E") == "" {
		t.Skip("set CONTAINARIUM_K8S_E2E=1 (and KUBECONFIG) to run the kind e2e")
	}
	kubeconfig := os.Getenv("KUBECONFIG")

	b, err := New(Config{
		Kubeconfig:   kubeconfig,
		BoxImage:     "registry.k8s.io/pause:3.9",
		GatewayHost:  "gateway.example.com",
		StorageClass: "standard", // kind's default local-path StorageClass
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("rest config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}

	ctx := context.Background()
	ref := box.BoxRef{Tenant: "csi-e2e"}
	ns := "tenant-csi-e2e"
	t.Cleanup(func() { _ = b.Purge(context.Background(), ref) })

	// Create: PVC provisioned, Sandbox pod template mounts it at dataMount
	// (/home/agent/workspace — not directly over /home/agent, see #974).
	if _, err := b.Create(ctx, box.BoxSpec{
		Ref:       ref,
		Image:     "registry.k8s.io/pause:3.9",
		SSHKeys:   []string{"ssh-ed25519 AAAAExampleKeyForCSIE2E test@csi-e2e"},
		Resources: box.ResourceLimits{Disk: "1Gi"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pvc, err := cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, pvcName, metav1.GetOptions{})
