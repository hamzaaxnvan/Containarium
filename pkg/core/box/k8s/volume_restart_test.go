//go:build k8s

package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	sandboxfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"
)

func volumeRestartSandbox() *sandboxv1beta1.Sandbox {
	return &sandboxv1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "tenant-test", UID: "worker-id", Generation: 5},
		Spec:       sandboxv1beta1.SandboxSpec{OperatingMode: sandboxv1beta1.SandboxOperatingModeRunning},
	}
}

func volumeRestartClient(t *testing.T, sb *sandboxv1beta1.Sandbox) *sandboxfake.Clientset {
	t.Helper()
	sc := sandboxfake.NewSimpleClientset()
	if _, err := sc.AgentsV1beta1().Sandboxes(sb.Namespace).Create(context.Background(), sb, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return sc
}

func TestVolumeRestartWaitsForCurrentSuspension(t *testing.T) {
	sb := volumeRestartSandbox()
	b, _, _ := testBackend()
