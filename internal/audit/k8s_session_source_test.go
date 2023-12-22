package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/footprintai/containarium/pkg/core/box/k8s/boxmeta"
)

func boxPod(namespace, name, tenant string, phase corev1.PodPhase) *corev1.Pod {
	labels := map[string]string{boxmeta.ManagedByLabel: boxmeta.ManagedByValue}
	if tenant != "" {
		labels[boxmeta.TenantLabel] = tenant
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func TestK8sSource_FindsRunningTenantBoxes(t *testing.T) {
	cs := fake.NewSimpleClientset(
		boxPod("tenant-alice", "alice-abc123", "alice", corev1.PodRunning),
		boxPod("tenant-bob", "bob-def456", "bob", corev1.PodRunning),
	)

	boxes, err := NewK8sSessionSource(cs, "").Boxes(context.Background())
	if err != nil {
		t.Fatalf("boxes: %v", err)
	}
	if len(boxes) != 2 {
		t.Fatalf("found %d boxes, want 2 — a box the collector never lists produces no login "+
			"records, which reads as if nobody logged in", len(boxes))
	}

	byName := map[string]SessionBox{}
	for _, b := range boxes {
		byName[b.Name] = b
	}
	alice, ok := byName["alice"]
	if !ok {
		t.Fatalf("boxes are not keyed by tenant: %+v", boxes)
