//go:build k8s

package k8s

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/footprintai/containarium/pkg/core/box"
)

// TestE2E_NetworkPolicyIsolation proves the per-tenant default-deny
// NetworkPolicy is actually ENFORCED, not merely created.
//
// Why this file exists (#1234): kind's default CNI (kindnet) does not enforce
// NetworkPolicy. #1195 tightened the policy — ingress restricted to the
// sshpiper pod, egress to cluster DNS — and merged on a green `kind e2e` that
// could not have failed, because nothing in the cluster was enforcing the
// object under test. A test that cannot fail is worse than no test: it reports
// confidence it hasn't earned.
//
// scripts/k8s-e2e.sh now installs Calico by default. When it doesn't
// (E2E_CNI=kindnet), this test SKIPS rather than passing — a vacuous pass is
// exactly the failure mode being fixed.
//
// The same trap has a second mouth, and avoiding it is most of this test:
// the real box pod runs registry.k8s.io/pause, which listens on NOTHING, so
// probing it would fail identically whether or not the policy blocks the
// connection. So the test stands up its own listener carrying the box labels
// the policy selects on, and runs a POSITIVE CONTROL from the gateway
// namespace first. If the allowed connection doesn't succeed, the denied one
// proves nothing and the test says so instead of reporting a pass.
func TestE2E_NetworkPolicyIsolation(t *testing.T) {
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
	// Create() programs an sshpiper Pipe when a gateway namespace is set, and
	// a gateway namespace is exactly what gives the policy its peer selectors
	// — so the CRD has to be here for this test to exercise the real path.
	installPipeCRD(t, dyn)

	b, err := New(Config{
		Kubeconfig:       kubeconfig,
		BoxImage:         "registry.k8s.io/pause:3.9",
		GatewayHost:      "gateway.example.com",
		GatewayNamespace: gatewayNS,
	})
	if err != nil {
		t.Fatalf("New (is the cluster reachable?): %v", err)
	}

	ctx := context.Background()
	ensureNamespace(ctx, t, b, gatewayNS)

	ref := box.BoxRef{Tenant: "netpol"}
	t.Cleanup(func() { _ = b.Delete(context.Background(), ref, true) })

	if _, err := b.Create(ctx, box.BoxSpec{Ref: ref}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	boxNS := b.cfg.TenantNamespacePrefix + ref.Tenant

	// Confirm the policy object carries real peer selectors. If a future change
	// regresses to port-only rules, the connectivity assertions below would
	// still behave correctly — but checking the object names the regression
	// precisely instead of leaving a bare connectivity failure to diagnose.
	np, err := b.clientset.NetworkingV1().NetworkPolicies(boxNS).Get(ctx, "default-deny", metav1.GetOptions{})
