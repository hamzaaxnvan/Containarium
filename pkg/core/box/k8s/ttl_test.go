//go:build k8s

package k8s

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	sandboxfake "sigs.k8s.io/agent-sandbox/clients/k8s/clientset/versioned/fake"

	"github.com/footprintai/containarium/pkg/core/box"
)

// TestSetTTLStampsShutdownTimeAndMeta verifies SetTTL writes both halves of
// the TTL contract in one patch: spec.shutdownTime (+ Retain policy) for the
// controller-side stop, and the ttl_expires_at meta annotation the daemon's
// sweeper reads via List/GetMeta.
func TestSetTTLStampsShutdownTimeAndMeta(t *testing.T) {
	b, _, sc := testBackend()
	ctx := context.Background()
	ref := box.BoxRef{Tenant: "ttl-user"}
