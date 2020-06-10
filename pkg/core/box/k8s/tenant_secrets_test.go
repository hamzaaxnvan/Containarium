package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/footprintai/containarium/pkg/core/box"
)

// sandboxPodSpecForTest returns the pod spec the backend would create.
func sandboxPodSpecForTest(t *testing.T, spec box.BoxSpec) corev1.PodSpec {
	t.Helper()
	sb := sandboxObject("tenant-"+spec.Ref.Tenant, spec, false, memDefaults{}, podOptions{})
	podSpec := sb.Spec.PodTemplate.Spec
	if len(podSpec.Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(podSpec.Containers))
	}
	return podSpec
}

func secretsBackend() *Backend {
	return NewWithClientset(fake.NewSimpleClientset(), nil, Config{TenantNamespacePrefix: "tenant-"})
}

func getTenantSecret(t *testing.T, b *Backend, tenant string) (*corev1.Secret, error) {
	t.Helper()
	return b.clientset.CoreV1().Secrets("tenant-"+tenant).
		Get(context.Background(), tenantSecretsName(tenant), metav1.GetOptions{})
}

func TestApplyTenantSecrets_CreatesTheSecret(t *testing.T) {
	b := secretsBackend()
	err := b.ApplyTenantSecrets(context.Background(), "alice", map[string][]byte{
		"API_TOKEN": []byte("s3cret"),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
