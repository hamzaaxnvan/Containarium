package threatdetect

import (
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func denyburstCtx(now time.Time, tenants map[string]string) RuleContext {
	return RuleContext{
		TenantForIP: fakeTenants(tenants),
		Now:         func() time.Time { return now },
	}
}

func TestDenyBurstRule_ID(t *testing.T) {
	r := NewDenyBurstRule(0, 0)
	if r.ID() != pb.ThreatRuleId_THREAT_RULE_ID_DENY_BURST {
		t.Errorf("ID() = %v, want THREAT_RULE_ID_DENY_BURST", r.ID())
	}
}

func TestNewDenyBurstRule_DefaultsOnZeroValue(t *testing.T) {
	r := NewDenyBurstRule(0, 0)
	if r.n != DefaultDenyBurstN {
		t.Errorf("n = %d, want default %d", r.n, DefaultDenyBurstN)
	}
	if r.window != DefaultDenyBurstWindow {
		t.Errorf("window = %v, want default %v", r.window, DefaultDenyBurstWindow)
	}
}

// OnDeny alone never fires — evaluation is deferred to Sweep (see the type
// doc). This must hold even once the threshold is technically met.
func TestDenyBurstRule_OnDeny_NeverFiresDirectly(t *testing.T) {
	r := NewDenyBurstRule(1, time.Minute)
	tenants := map[string]string{"10.0.0.1": "alice"}
	base := time.Unix(1000, 0)

	for i := 0; i < 5; i++ {
		ev := netbpf.DenyEvent{Saddr: beIP(10, 0, 0, 1), Daddr: beIP(10, 0, 0, 2)}
		if got := r.OnDeny(denyburstCtx(base, tenants), ev); got != nil {
			t.Fatalf("OnDeny returned %+v, want nil (Sweep-driven, not signal-driven)", got)
		}
	}
}

// Unresolved source IP: dropped, never attributed to a guessed tenant.
func TestDenyBurstRule_OnDeny_UnknownSourceDropped(t *testing.T) {
	r := NewDenyBurstRule(1, time.Minute)
