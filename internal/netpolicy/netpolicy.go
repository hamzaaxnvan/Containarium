// Package netpolicy validates and compiles per-tenant NetworkPolicy values
// (the Phase A control-plane half of the eBPF network-isolation design) into
// the normalized form the per-veth TC_INGRESS BPF loader consumes.
//
// It is deliberately pure (no BPF, no DB, no network): given a *pb.NetworkPolicy
// it returns either a validation error or a CompiledPolicy with parsed/deduped
// CIDRs, normalized domains, and a resolved mode. The BPF map-update plumbing
// (a later Phase A increment) turns a CompiledPolicy into map entries. See
// docs/security/NETWORK-ISOLATION-DESIGN.md (#315).
package netpolicy

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/safecast"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CompiledPolicy is the normalized, validated form of a NetworkPolicy, ready for
// the BPF loader. Egress is allowed to any EgressCIDRs plus the CIDRs the
// daemon resolves from EgressDomains; intra-tenant peer traffic is gated by
// AllowIntraTenant.
type CompiledPolicy struct {
	Tenant string
	// IsHostDefault marks the host's default egress policy (DefaultPolicy), as
	// opposed to one compiled from a stored policy (#2996).
	IsHostDefault    bool
	AllowIntraTenant bool
	EgressCIDRs      []netip.Prefix // parsed, masked-to-network, deduped, sorted
	EgressDomains    []string       // lowercased, trimmed, deduped, sorted
	AllowMetadata    bool           // may reach the cloud metadata service (default deny)
	Mode             pb.NetworkPolicyMode
	// LogOnly is true unless Mode is ENFORCE — i.e. UNSPECIFIED and LOG_ONLY
	// both observe-only (Phase A default), only ENFORCE drops packets.
	LogOnly bool
	// DenyRules are virtual-patch block rules (#660): parsed/masked/deduped/sorted
	// destination prefixes (optionally port/proto-scoped) that are denied BEFORE
	// the egress allow-list is consulted — deny beats allow. Expiry is preserved
	// here (not filtered) so the package stays time-pure; the daemon drops expired
	// rules with DenyRule.Expired(now) before pushing them to the kernel.
	DenyRules []DenyRule
	// AllowFromTenants (#2359) lists tenants whose boxes may reach this
	// tenant's boxes; trimmed, deduped, sorted, never the tenant itself.
	// Consumed by the tenant network guard, not by the eBPF program.
	AllowFromTenants []string
	// EgressPreset (#2440) is the ready-made egress posture; UNSPECIFIED is
	// "just the lists". The implicit allow it implies (the DNS resolver) is
	// added by ImplicitEgress, because the address is the daemon's to know.
	EgressPreset pb.EgressPreset
	// EgressMode is the enforcement mode of the egress allow-list alone; Mode
	// keeps governing every other check. Carried as given: UNSPECIFIED means
	// "follow Mode" and is resolved by EffectiveEgressMode, so a stored policy
	// written before the field existed keeps behaving exactly as it did.
	EgressMode pb.NetworkPolicyMode
}

// EffectiveEgressMode is the mode the egress allow-list decision runs under:
// EgressMode when set, otherwise Mode (already resolved UNSPECIFIED→LOG_ONLY by
// Compile).
func (c CompiledPolicy) EffectiveEgressMode() pb.NetworkPolicyMode {
	if c.EgressMode != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED {
		return c.EgressMode
	}
	if c.Mode == pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED {
		return pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY
	}
	return c.Mode
}

// DenyRule is one normalized virtual-patch block rule (#660). The destination
// CIDR is matched first; Port/Proto (0 = any) further scope the block to a
// single service. A host IP is carried as a /32.
type DenyRule struct {
	CIDR      netip.Prefix
	Port      uint16    // 0 = any port
	Proto     uint8     // IP protocol number (0 = any; 6 = tcp, 17 = udp)
	Note      string    // operator note, typically a CVE id
	ExpiresAt time.Time // zero = no expiry
}

// Expired reports whether the rule's expiry has passed relative to now. A
// zero ExpiresAt never expires.
func (d DenyRule) Expired(now time.Time) bool {
	return !d.ExpiresAt.IsZero() && now.After(d.ExpiresAt)
}

// Validate reports whether a NetworkPolicy is well-formed without compiling it.
// Compile performs the same checks, so callers that only need the compiled
// result can skip Validate.
func Validate(p *pb.NetworkPolicy) error {
	_, err := Compile(p)
	return err
}

// Compile validates and normalizes a NetworkPolicy.
func Compile(p *pb.NetworkPolicy) (CompiledPolicy, error) {
	if p == nil {
		return CompiledPolicy{}, fmt.Errorf("network policy is nil")
	}
	tenant := strings.TrimSpace(p.GetTenant())
	if tenant == "" {
		return CompiledPolicy{}, fmt.Errorf("network policy: tenant is required")
	}

	cidrs, err := compileCIDRs(p.GetEgressCidrs())
	if err != nil {
		return CompiledPolicy{}, err
	}
	domains, err := compileDomains(p.GetEgressDomains())
	if err != nil {
		return CompiledPolicy{}, err
	}
	deny, err := compileDenyRules(p.GetDenyRules())
	if err != nil {
		return CompiledPolicy{}, err
	}
	allowFrom, err := compileAllowFromTenants(tenant, p.GetAllowFromTenants())
	if err != nil {
		return CompiledPolicy{}, err
	}

	// Unspecified defaults to log-only in Phase A; reject unknown enum values.
	mode := p.GetMode()
	switch mode {
	case pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED:
		mode = pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY
	case pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE:
		// ok
	default:
		return CompiledPolicy{}, fmt.Errorf("network policy: unknown mode %d", int32(mode))
	}

	// egress_mode: UNSPECIFIED is kept (it means "follow mode"); unknown values
	// are rejected like an unknown mode.
	switch p.GetEgressMode() {
	case pb.NetworkPolicyMode_NETWORK_POLICY_MODE_UNSPECIFIED,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
		pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE:
	default:
		return CompiledPolicy{}, fmt.Errorf("network policy: unknown egress mode %d", int32(p.GetEgressMode()))
	}

	switch p.GetEgressPreset() {
	case pb.EgressPreset_EGRESS_PRESET_UNSPECIFIED, pb.EgressPreset_EGRESS_PRESET_ALLOW_LIST_ONLY:
	default:
		return CompiledPolicy{}, fmt.Errorf("network policy: unknown egress preset %d", int32(p.GetEgressPreset()))
	}

	return CompiledPolicy{
