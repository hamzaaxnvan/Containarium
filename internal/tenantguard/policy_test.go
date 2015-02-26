package tenantguard

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func baseInputs(boxes ...Box) Inputs {
	return Inputs{
		BridgeCIDR:  netip.MustParsePrefix("10.100.0.0/24"),
		HostGateway: addr("10.100.0.1"),
		Initiators: map[incus.Role][]netip.Addr{
			incus.RoleCaddy:        {addr("10.100.0.241")},
			incus.RoleControlPlane: {addr("10.100.0.200")},
		},
		Boxes: boxes,
	}
}

func sources(acl incus.ACLConfig) []string {
	out := make([]string, 0, len(acl.IngressRules))
	for _, r := range acl.IngressRules {
		out = append(out, r.Source)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCompute_SingleBoxTenantHasNoSiblingRule(t *testing.T) {
	pol, err := Compute(baseInputs(Box{Name: "alice-container", Tenant: "alice", IPv4: addr("10.100.0.17")}))
	if err != nil {
		t.Fatal(err)
	}
	acl := pol.ACLs["alice"]
	want := []string{"10.100.0.1/32", "10.100.0.241/32", "10.100.0.200/32", "10.100.0.17/32"}
	if got := sources(acl); !equalStrings(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
	for _, r := range acl.IngressRules {
		if r.Action != "allow" || r.Protocol != "" || r.DestinationPort != "" {
			t.Errorf("rule should allow every protocol/port from the source: %+v", r)
		}
	}
	if len(acl.EgressRules) != 0 {
		t.Errorf("egress must stay the eBPF enforcer's job, got %+v", acl.EgressRules)
	}
}

func TestCompute_SiblingsAndAddressless(t *testing.T) {
	pol, err := Compute(baseInputs(
		Box{Name: "a-web", Tenant: "alice", IPv4: addr("10.100.0.17")},
		Box{Name: "a-db", Tenant: "alice", IPv4: addr("10.100.0.18"), IPv6: []netip.Addr{addr("fd42::18"), addr("fd42::17")}},
		Box{Name: "a-new", Tenant: "alice"}, // no address yet
	))
	if err != nil {
		t.Fatal(err)
	}
	acl := pol.ACLs["alice"]
	// host → initiators → siblings by name, v4 before v6, v6 sorted.
	want := []string{
		"10.100.0.1/32", "10.100.0.241/32", "10.100.0.200/32",
		"10.100.0.18/32", "fd42::17/128", "fd42::18/128", // a-db
		"10.100.0.17/32", // a-web
	}
	if got := sources(acl); !equalStrings(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
	if names := pol.ByTenant["alice"]; !equalStrings(names, []string{"a-db", "a-new", "a-web"}) {
		t.Errorf("ByTenant = %v", names)
	}
}

func TestCompute_TwoTenantsNeverCross(t *testing.T) {
	pol, err := Compute(baseInputs(
		Box{Name: "alice-container", Tenant: "alice", IPv4: addr("10.100.0.17")},
		Box{Name: "bob-container", Tenant: "bob", IPv4: addr("10.100.0.33")},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(pol.ACLs) != 2 {
		t.Fatalf("ACLs = %d, want 2", len(pol.ACLs))
	}
	for _, s := range sources(pol.ACLs["alice"]) {
		if s == "10.100.0.33/32" {
			t.Error("alice's ACL admits bob")
		}
	}
	for _, s := range sources(pol.ACLs["bob"]) {
		if s == "10.100.0.17/32" {
			t.Error("bob's ACL admits alice")
		}
	}
	if pol.ACLs["alice"].Name == pol.ACLs["bob"].Name {
		t.Error("tenants share an ACL name")
	}
}

func TestCompute_Initiators(t *testing.T) {
	// Only caddy and the control plane may open connections to tenants;
	// a postgres address in Initiators is ignored (the table, not the
	// caller, decides).
	in := baseInputs(Box{Name: "alice-container", Tenant: "alice", IPv4: addr("10.100.0.17")})
	in.Initiators[incus.RolePostgres] = []netip.Addr{addr("10.100.0.242")}
	pol, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
