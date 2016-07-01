//go:build k8s

package k8s

import (
	"context"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/footprintai/containarium/pkg/core/box"
)

// TestE2E_SharedVolumeRestartsRunningPod checks the live Pod, rather than just
// the Sandbox template, after attach, detach, and force-delete. A static
// hostPath PV supplies RWX metadata on the single-node kind cluster; this test
// exercises controller lifecycle behavior, not a distributed RWX provisioner.
func TestE2E_SharedVolumeRestartsRunningPod(t *testing.T) {
	if os.Getenv("CONTAINARIUM_K8S_E2E") == "" {
		t.Skip("set CONTAINARIUM_K8S_E2E=1 (and KUBECONFIG) to run the kind e2e")
	}
	const class = "containarium-e2e-volume-restart"
