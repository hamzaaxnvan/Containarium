//go:build k8s

package k8s

import (
	"context"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/footprintai/containarium/pkg/core/box"
)

// TestE2E_BoxEgressPosture observes what a box can actually reach outbound.
//
// This closes the last open acceptance criterion on #1234: "document the
// current no-outbound-egress posture once observed, or fix it under #1188 if
// it turns out to be unintended." Until Calico landed, nobody had observed it
// — the policy was created and ignored, so the posture was inferred from
// reading the object rather than from behavior.
//
// What the object says: exactly ONE egress rule, DNS (UDP/TCP 53) to the
// kube-system namespace. Everything else denied. If that is what the cluster
// actually enforces, a K8s box can resolve names and connect to nothing —
// no package installs, no outbound HTTP, no control-plane API. That is a
// significant property for an agent runtime whose pitch is shell_exec, and
// it should be a measured fact rather than an assumption.
//
// Structure follows the lesson from #1235: the DENIAL assertion is worthless
// without a POSITIVE CONTROL, because a pod with no network at all would fail
// both. DNS is the control — the one thing the policy is supposed to allow.
func TestE2E_BoxEgressPosture(t *testing.T) {
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
		t.Fatalf("dynamic client: %v", err)
	}
	installPipeCRD(t, dyn)

	b, err := New(Config{
		Kubeconfig:       kubeconfig,
		BoxImage:         "registry.k8s.io/pause:3.9",
		GatewayHost:      "gateway.example.com",
