package server

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// defaultsTestStore overrides List on a real in-memory store, so a test can hand the
// enforcer a stored policy that no longer compiles, or a store outage.
type defaultsTestStore struct {
	*MemNetworkPolicyStore
	list []*pb.NetworkPolicy
	err  error
}

func (s defaultsTestStore) List(context.Context) ([]*pb.NetworkPolicy, error) { return s.list, s.err }

func enforcerWithStore(st NetworkPolicyStore) *NetworkPolicyEnforcer {
	return NewNetworkPolicyEnforcer("", st, nil, nil, nil, nil, true)
}

// #2997: the policies a reconcile pass plans from. A stored policy beats the
// default, the default only applies when the posture is on, and a store outage
// fails the pass without applying any default.
func TestPlanPolicies(t *testing.T) {
	enforce := &pb.NetworkPolicy{Tenant: "stored", Mode: pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE}
	views := []containerView{{Tenant: "stored"}, {Tenant: "fresh"}}

	t.Run("stored policy beats the default", func(t *testing.T) {
		e := enforcerWithStore(defaultsTestStore{MemNetworkPolicyStore: NewMemNetworkPolicyStore(), list: []*pb.NetworkPolicy{enforce}})
		if err := e.SetDefaultEgress(netpolicy.DefaultEgressExtras{}); err != nil {
			t.Fatal(err)
		}
		got, err := e.planPolicies(context.Background(), views)
		if err != nil {
			t.Fatal(err)
		}
		if got["stored"].Mode != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE {
			t.Errorf("stored policy was replaced by the default: %+v", got["stored"])
		}
		if d, ok := got["fresh"]; !ok || !d.LogOnly {
			t.Errorf("tenant with no stored policy = %+v, %v; want the log-only default", d, ok)
		}
	})

	t.Run("posture off adds nothing", func(t *testing.T) {
		e := enforcerWithStore(defaultsTestStore{MemNetworkPolicyStore: NewMemNetworkPolicyStore(), list: []*pb.NetworkPolicy{enforce}})
		got, err := e.planPolicies(context.Background(), views)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got["fresh"]; ok || len(got) != 1 {
			t.Errorf("default applied with the posture off: %v", got)
		}
	})

	t.Run("store outage fails the pass and applies no default", func(t *testing.T) {
