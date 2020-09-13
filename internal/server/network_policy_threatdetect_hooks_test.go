package server

import (
	"context"
	"net/netip"
	"testing"

	"github.com/footprintai/containarium/internal/netbpf"
)

// pollFlows only runs its flow-hook call when e.loader is non-nil (a real
// eBPF object), which a unit test can't construct — so this exercises the
// hook plumbing directly instead, the same way pollFlows itself invokes it.
func TestFlowHook_InvokedWithActiveBatch(t *testing.T) {
	e := NewNetworkPolicyEnforcer("", nil, NewMemTenantRegistry(), nil, nil, nil, false)

	var got []netbpf.FlowRecord
	e.SetFlowHook(func(flows []netbpf.FlowRecord) { got = flows })
	if e.flowHook == nil {
		t.Fatal("SetFlowHook did not store the hook")
	}

	batch := []netbpf.FlowRecord{{Saddr: 1, Daddr: 2}}
	e.flowHook(batch)
