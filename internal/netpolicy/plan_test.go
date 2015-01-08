package netpolicy

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func row(t time.Time, dst string, proto, dport int, dropped, vpatch bool) DenyRow {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return DenyRow{Time: t, Detail: `{"src":"10.0.0.5","dst":"` + dst + `","proto":` + itoa(proto) +
		`,"dport":` + itoa(dport) + `,"dropped":` + b(dropped) + `,"virtual_patch":` + b(vpatch) + `}`}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAggregateDenies_GroupsCountsAndOrders(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	rows := []DenyRow{
		row(t0, "140.82.112.3", 6, 443, false, false),
		row(t0.Add(time.Minute), "140.82.112.3", 6, 443, true, false),
		row(t0.Add(2*time.Minute), "140.82.112.3", 6, 443, false, false),
		row(t0, "8.8.8.8", 17, 53, false, false),
		row(t0, "140.82.112.3", 6, 22, false, false), // same IP, different port: separate row
	}
	got := AggregateDenies(rows, nil)
	if len(got) != 3 {
		t.Fatalf("got %d destinations: %+v", len(got), got)
	}
	if got[0].IP.String() != "140.82.112.3" || got[0].Port != 443 || got[0].Proto != "tcp" || got[0].Count != 3 {
		t.Errorf("busiest = %+v", got[0])
	}
	if !got[0].Dropped {
		t.Error("one dropped flow must mark the destination dropped")
	}
	if !got[0].LastSeen.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("LastSeen = %v", got[0].LastSeen)
	}
	if got[1].Count != 1 || got[2].Count != 1 {
		t.Errorf("tail = %+v", got[1:])
	}
}

func TestAggregateDenies_SkipsVirtualPatchGarbageAndAllowed(t *testing.T) {
	t0 := time.Now()
	rows := []DenyRow{
		row(t0, "1.2.3.4", 6, 6379, true, true), // operator's deliberate block
		{Time: t0, Detail: "not json"},
		{Time: t0, Detail: `{"dst":"not-an-ip"}`},
		{Time: t0, Detail: `{"dst":"2001:db8::1"}`}, // IPv6: out of scope
		row(t0, "10.1.2.3", 6, 80, false, false),    // already allowed below
		row(t0, "9.9.9.9", 17, 53, false, false),
	}
	allowed := func(ip netip.Addr) bool { return netip.MustParsePrefix("10.0.0.0/8").Contains(ip) }
	got := AggregateDenies(rows, allowed)
	if len(got) != 1 || got[0].IP.String() != "9.9.9.9" {
		t.Fatalf("got %+v", got)
	}
}

func TestAggregateDenies_ProtocolLabelsAndPortBounds(t *testing.T) {
	t0 := time.Now()
	got := AggregateDenies([]DenyRow{
		row(t0, "5.5.5.5", 1, 0, false, false),
