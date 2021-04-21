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
