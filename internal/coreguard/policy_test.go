package coreguard

import (
	"net/netip"
	"sort"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// fullInputs is a backend with every core role present, a control plane
// co-located on the bridge, and the daemon at the bridge gateway — the
// richest topology the table has to render.
func fullInputs() Inputs {
	return Inputs{
		BridgeCIDR:  netip.MustParsePrefix("10.100.0.0/24"),
		HostGateway: netip.MustParseAddr("10.100.0.1"),
		Core: map[incus.Role]netip.Addr{
			incus.RolePostgres:        netip.MustParseAddr("10.100.0.242"),
			incus.RoleVictoriaMetrics: netip.MustParseAddr("10.100.0.243"),
			incus.RoleOTelCollector:   netip.MustParseAddr("10.100.0.244"),
			incus.RoleSecurity:        netip.MustParseAddr("10.100.0.245"),
			incus.RoleGuacamole:       netip.MustParseAddr("10.100.0.246"),
			incus.RoleCaddy:           netip.MustParseAddr("10.100.0.241"),
		},
		ControlPlane: []netip.Addr{netip.MustParseAddr("10.100.0.200")},
	}
}

// rule is the comparable shape a test pins: who may reach which tcp ports.
type rule struct{ src, ports string }

func rulesOf(acl incus.ACLConfig) []rule {
	out := make([]rule, 0, len(acl.IngressRules))
	for _, r := range acl.IngressRules {
		out = append(out, rule{src: r.Source, ports: r.DestinationPort})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ports != out[j].ports {
			return out[i].ports < out[j].ports
		}
		return out[i].src < out[j].src
	})
	return out
}

func TestCompute_RoleTable(t *testing.T) {
	const (
		host   = "10.100.0.1/32"
		bridge = "10.100.0.0/24"
		cp     = "10.100.0.200/32"
		caddy  = "10.100.0.241/32"
		vm     = "10.100.0.243/32"
	)
	tests := []struct {
		role incus.Role
		want []rule
	}{
		{incus.RolePostgres, []rule{{host, "5432"}, {vm, "5432"}}},
		{incus.RoleVictoriaMetrics, []rule{{host, "3000"}, {caddy, "3000"}, {host, "8428,8880,9093,9094"}, {cp, "8428,8880,9093,9094"}}},
		{incus.RoleOTelCollector, []rule{{host, "13133,8888"}, {bridge, "4317,4318"}}},
		{incus.RoleCaddy, []rule{{host, "2019"}, {bridge, "80,443"}}},
		{incus.RoleSecurity, []rule{}},
		{incus.RoleGuacamole, []rule{{host, "8080"}, {caddy, "8080"}}},
	}

	pol, err := Compute(fullInputs())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(pol.UnknownRoles) != 0 {
		t.Fatalf("unexpected unknown roles: %v", pol.UnknownRoles)
	}
	if got, want := len(pol.ACLs), len(tests); got != want {
		t.Fatalf("Compute produced %d ACLs, want %d: %v", got, want, keysOf(pol.ACLs))
	}

	for _, tc := range tests {
		t.Run(string(tc.role), func(t *testing.T) {
			acl, ok := pol.ACLs[tc.role]
			if !ok {
				t.Fatalf("no ACL for %s", tc.role)
			}
			if acl.Name != ACLName(tc.role) {
				t.Errorf("ACL name = %q, want %q", acl.Name, ACLName(tc.role))
			}
			if len(acl.EgressRules) != 0 {
				t.Errorf("egress rules must be empty (egress is left open via the NIC default), got %v", acl.EgressRules)
			}
			got := rulesOf(acl)
			want := append([]rule(nil), tc.want...)
			sort.Slice(want, func(i, j int) bool {
				if want[i].ports != want[j].ports {
					return want[i].ports < want[j].ports
				}
				return want[i].src < want[j].src
			})
			if len(got) != len(want) {
				t.Fatalf("rules = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("rule[%d] = %v, want %v (all: %v)", i, got[i], want[i], got)
				}
			}
			for _, r := range acl.IngressRules {
				if r.Action != "allow" || r.Protocol != "tcp" {
					t.Errorf("rule %+v: every table rule is an explicit tcp allow", r)
				}
			}
		})
	}
