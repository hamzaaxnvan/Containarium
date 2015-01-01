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
