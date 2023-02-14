package server

import (
	"context"
	"errors"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #1188 AC1 and AC3: NetworkPolicyService changes reach the K8s backend, and
// a removed policy reverts to the default-deny floor.

// recordingApplier captures what the reconciler asked the backend to apply.
type recordingApplier struct {
	mu    sync.Mutex
	calls []applyCall
	err   error
}

type applyCall struct {
	tenant string
	policy *pb.NetworkPolicy
}

func (a *recordingApplier) ApplyTenantPolicy(_ context.Context, tenant string, p *pb.NetworkPolicy) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, applyCall{tenant: tenant, policy: p})
	return a.err
}

func (a *recordingApplier) since(n int) []applyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n >= len(a.calls) {
		return nil
	}
	return append([]applyCall(nil), a.calls[n:]...)
}

// listStore is a NetworkPolicyStore whose List is all the reconciler uses.
type listStore struct {
	policies []*pb.NetworkPolicy
	err      error
}

func (s *listStore) List(context.Context) ([]*pb.NetworkPolicy, error) {
	return s.policies, s.err
}
func (s *listStore) Set(context.Context, *pb.NetworkPolicy) error { return nil }
func (s *listStore) Get(context.Context, string) (*pb.NetworkPolicy, error) {
	return nil, errors.New("not used")
}
func (s *listStore) Delete(context.Context, string) error { return nil }
func (s *listStore) MutateDenyRules(context.Context, string,
	func([]*pb.NetworkPolicyDenyRule) ([]*pb.NetworkPolicyDenyRule, error)) (*pb.NetworkPolicy, error) {
	return nil, errors.New("not used")
}

func TestK8sNetPolicyReconcile_AppliesEachTenantsPolicy(t *testing.T) {
	store := &listStore{policies: []*pb.NetworkPolicy{
		{Tenant: "alice", EgressCidrs: []string{"10.0.0.0/8"}},
		{Tenant: "bob", EgressCidrs: []string{"192.168.0.0/16"}},
	}}
	applier := &recordingApplier{}
	r := NewK8sNetworkPolicyReconciler(store, applier, 0)

	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(applier.calls) != 2 {
