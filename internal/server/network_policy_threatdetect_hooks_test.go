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
	if len(got) != 1 || got[0] != batch[0] {
		t.Fatalf("flow hook received %+v, want %+v", got, batch)
	}
}

// #1640: OnDenyEvent must hand every deny event to a registered deny hook,
// in addition to (not instead of) its existing audit-log write.
func TestOnDenyEvent_CallsDenyHook(t *testing.T) {
	e := NewNetworkPolicyEnforcer("", nil, NewMemTenantRegistry(), nil, nil, nil, false)

	var got []netbpf.DenyEvent
	e.SetDenyHook(func(ev netbpf.DenyEvent) { got = append(got, ev) })

	ev := netbpf.DenyEvent{TenantID: 7, Dport: 443, Proto: 6}
	e.OnDenyEvent(context.Background(), ev)

	// No enforce entry for tenant 7, so the hook sees it as observation-only.
	want := ev
	want.LogOnly = true
	if len(got) != 1 || got[0] != want {
		t.Fatalf("deny hook calls = %+v, want exactly one call with %+v", got, want)
	}
}

// #2612: the hook must learn whether the tenant's policy is enforced, since
