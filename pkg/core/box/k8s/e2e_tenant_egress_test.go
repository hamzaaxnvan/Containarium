//go:build k8s

package k8s

import (
	"context"
	"fmt"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #1188 AC5: a tenant egress allowlist is ENFORCED on the K8s backend — a
// destination outside it is genuinely unreachable, not merely absent from a
// YAML object.
//
// This is the assertion the whole issue turns on. A tenant who sets an egress
// allowlist gets it enforced on LXC and, until now, silently ignored here.
// Checking that the NetworkPolicy object has the right shape would not
// distinguish "enforced" from "written and ignored" — which is exactly how
// #1195 shipped unvalidated and became #1234.
//
// The test therefore asserts both directions, and the ALLOWED probe runs
// first. Without it, "the denied destination was unreachable" is equally
// consistent with the CNI dropping everything, the listener never starting,
// or the wrong port — and the test would pass while proving nothing.
func TestE2E_TenantEgressAllowlistIsEnforced(t *testing.T) {
	if os.Getenv("CONTAINARIUM_K8S_E2E") == "" {
		t.Skip("set CONTAINARIUM_K8S_E2E=1 (and KUBECONFIG) to run the kind e2e")
	}
	if cni := os.Getenv("E2E_CNI"); cni == "kindnet" {
		t.Skip("E2E_CNI=kindnet does not enforce NetworkPolicy; skipping rather than passing vacuously (#1234)")
	}

	const gatewayNS = "agent-gateway"
	kubeconfig := os.Getenv("KUBECONFIG")

	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("rest config: %v", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
