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
