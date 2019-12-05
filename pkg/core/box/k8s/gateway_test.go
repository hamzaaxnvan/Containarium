//go:build k8s

package k8s

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	sandboxfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/footprintai/containarium/pkg/core/box"
)

// gatewayBackend wires a Backend with both a fake typed clientset and a fake
// dynamic client that knows the Pipe GVR, with the gateway namespace set so
// Pipe reconciliation is active.
func gatewayBackend() (*Backend, *dynfake.FakeDynamicClient) {
	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{pipeGVR: "PipeList"})
	b := NewWithClients(fake.NewSimpleClientset(), sandboxfake.NewSimpleClientset(), dyn, Config{
		GatewayNamespace: "sshpiper",
		GatewayHost:      "gw.example.com",
	})
	return b, dyn
}

func getPipe(t *testing.T, dyn *dynfake.FakeDynamicClient, tenant string) *unstructured.Unstructured {
	t.Helper()
	p, err := dyn.Resource(pipeGVR).Namespace("sshpiper").Get(context.Background(), pipeName(tenant), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pipe %s: %v", pipeName(tenant), err)
	}
	return p
}

func TestCreateProgramsPipe(t *testing.T) {
	b, dyn := gatewayBackend()
	ctx := context.Background()
	if _, err := b.Create(ctx, box.BoxSpec{
		Ref:       box.BoxRef{Tenant: "alice"},
		Image:     "x",
		SSHKeys:   []string{"ssh-ed25519 AAAA"},
		AutoStart: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	p := getPipe(t, dyn, "alice")

	from, _, _ := unstructured.NestedSlice(p.Object, "spec", "from")
	if len(from) != 1 {
		t.Fatalf("spec.from len = %d, want 1", len(from))
	}
	f0 := from[0].(map[string]any)
