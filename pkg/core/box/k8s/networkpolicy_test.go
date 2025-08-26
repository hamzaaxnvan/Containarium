package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// The gap these tests close (#1193): the shipped NetworkPolicy carried rules
// with `ports` and no `from`/`to`. Per the NetworkPolicy spec that matches
// every source and destination, so tenant boxes were reachable on :22 from any
// pod in the cluster while the design note said the opposite. Nothing asserted
// the object's shape, so code and doc drifted silently.
//
// The load-bearing assertion in each test below is that the peer list is
// NON-EMPTY. A port-only rule must fail these.

func TestNetworkPolicyIngressRestrictedToGateway(t *testing.T) {
	np := networkPolicyObject("tenant-a", "alice", "agent-gateway")

	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("want exactly 1 ingress rule, got %d", len(np.Spec.Ingress))
	}
	rule := np.Spec.Ingress[0]

	if len(rule.From) == 0 {
