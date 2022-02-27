package k8s

import (
	"context"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	sandboxfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"
)

// #1188: a tenant's egress policy is enforced on LXC and silently ignored on
// K8s, with the same SetNetworkPolicy call succeeding either way.
//
// The compiler's job is to carry what it can and REFUSE what it cannot.
// Every one of the stored model's inexpressible features fails in the SAME
// direction — more permissive than the tenant asked for — so dropping any of
// them quietly is strictly worse than not supporting the backend at all.

func enforcing(p *pb.NetworkPolicy) *pb.NetworkPolicy {
	p.Mode = pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE
	return p
}

func policyBackend() *Backend {
	return NewWithClientset(fake.NewSimpleClientset(), sandboxfake.NewSimpleClientset(),
		Config{TenantNamespacePrefix: "tenant-", GatewayNamespace: "agent-gateway"})
}

// THE most dangerous mis-compilation. LOG_ONLY means "observe denied flows,
// drop nothing" — a dry run. NetworkPolicy has no observe-only mode, so
// applying one ENFORCES it. Compiling a LOG_ONLY policy would start dropping
// a tenant's traffic during what they intended as a trial, and it would be
// discovered as an outage.
func TestCompileTenantPolicy_RefusesLogOnlyMode(t *testing.T) {
	for _, mode := range []pb.NetworkPolicyMode{
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED, // treated as LOG_ONLY
	} {
		_, unsupported := compileTenantPolicy(&pb.NetworkPolicy{
			Tenant: "alice", Mode: mode, EgressCidrs: []string{"10.0.0.0/8"},
		})
		if len(unsupported) == 0 {
			t.Errorf("mode %v compiled to an enforcing NetworkPolicy — the tenant asked to "+
				"observe, and this would start dropping their traffic", mode)
		}
	}
}

// The metadata carve-out is deny-beats-allow and guards cloud credentials.
//
