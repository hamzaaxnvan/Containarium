package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake "k8s.io/client-go/kubernetes/fake"

	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	sandboxfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/footprintai/containarium/pkg/core/box"
)

// resizeBackend wires a Backend over a fake Sandbox already carrying the given
// resource requirements on its agent-box container, so a Resize can be observed
// against a known starting state.
func resizeBackend(t *testing.T, tenant string, start corev1.ResourceRequirements) (*Backend, *sandboxfake.Clientset) {
	t.Helper()
	sc := sandboxfake.NewSimpleClientset()
	if _, err := sc.AgentsV1beta1().Sandboxes("containarium-"+tenant).Create(context.Background(), &sandboxv1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: sandboxName, Namespace: "containarium-" + tenant},
		Spec: sandboxv1beta1.SandboxSpec{
			SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{
				PodTemplate: sandboxv1beta1.PodTemplate{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "agent-box", Resources: start}},
					},
				},
			},
		},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed sandbox: %v", err)
	}
	b := NewWithClients(fake.NewSimpleClientset(), sc, nil, Config{TenantNamespacePrefix: "containarium-"})
	return b, sc
}

func resizedResources(t *testing.T, sc *sandboxfake.Clientset, tenant string) corev1.ResourceRequirements {
	t.Helper()
	sb, err := sc.AgentsV1beta1().Sandboxes("containarium-"+tenant).
		Get(context.Background(), sandboxName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	for _, c := range sb.Spec.PodTemplate.Spec.Containers {
		if c.Name == "agent-box" {
			return c.Resources
		}
	}
	t.Fatal("agent-box container not found")
	return corev1.ResourceRequirements{}
}

func qty(t *testing.T, s string) resource.Quantity {
	t.Helper()
	q, err := resource.ParseQuantity(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return q
}

// A resize that names only CPU must leave the box's memory alone. Resize's own
// contract is "empty = unchanged" — the doc comment says so explicitly about
// the memory default — so an unmentioned axis must survive.
