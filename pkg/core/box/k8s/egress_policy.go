package k8s

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Compiling a tenant's egress ACL down to Kubernetes NetworkPolicy (#1188).
//
// On the LXC path a tenant's policy becomes eBPF: an egress allowlist, deny
// CIDRs, virtual-patch rules. On K8s none of it was expressed — a tenant who
// set an egress allowlist got it enforced on LXC and silently ignored on K8s,
// with the same SetNetworkPolicy call succeeding either way.
//
// # What cannot be carried across, and why that must be loud
//
// Kubernetes NetworkPolicy is ALLOW-ONLY. There is no deny rule: a policy is
// a set of permitted flows, and everything unlisted is denied by the presence
// of the policy itself. So an explicit DROP/REJECT rule has no equivalent.
//
// Dropping such a rule on the floor would reproduce exactly the bug this
// exists to fix — a tenant's stated intent honoured on one backend and
// quietly ignored on the other. So the compiler REFUSES to compile a policy
// it cannot express faithfully, and names the rules it could not carry. A
// caller can then reject the policy, or record the asymmetry, but it cannot
// fail to notice it.
//
// The narrower reading — "a deny rule is redundant under default-deny, so it
// is safe to skip" — is true only when the denied destination is not also
// matched by an allow rule. It is precisely wrong for the case tenants
// actually write: allow a wide range, then carve an exception out of it.

// UnsupportedPolicyFeature is part of a tenant policy that has no faithful
// NetworkPolicy expression.
type UnsupportedPolicyFeature struct {
	Feature string
	Reason  string
}

func (u UnsupportedPolicyFeature) String() string {
	return fmt.Sprintf("%s (%s)", u.Feature, u.Reason)
}

// metadataIP is the cloud metadata service. The stored policy denies it even
// when the egress allowlist would otherwise cover it, because it hands out
// cloud credentials.
var metadataIP = netip.MustParseAddr("169.254.169.254")

// compileTenantPolicy turns the stored tenant network policy into
// NetworkPolicy egress rules, returning everything it could not express.
//
// A non-empty unsupported list means the policy MUST NOT be applied: the
// result would differ from what the tenant configured, in the permissive
// direction every time.
func compileTenantPolicy(p *pb.NetworkPolicy) ([]networkingv1.NetworkPolicyEgressRule, []UnsupportedPolicyFeature) {
	if p == nil {
		return nil, nil
	}

	var unsupported []UnsupportedPolicyFeature

	// LOG_ONLY asks to observe denied flows and drop nothing. NetworkPolicy
	// has no observe-only mode: applying one enforces it. Compiling a
	// LOG_ONLY policy would therefore start dropping traffic during what the
	// tenant intended as a dry run — the exact opposite of the request, and
	// the kind of thing discovered as an outage.
	switch p.GetMode() {
	case pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE:
		// the only mode NetworkPolicy can honour
	default:
		unsupported = append(unsupported, UnsupportedPolicyFeature{
			Feature: fmt.Sprintf("mode=%s", p.GetMode()),
			Reason:  "NetworkPolicy always enforces; it cannot observe without dropping",
		})
	}

	// Domains are resolved to CIDRs on a refresh loop on the eBPF path.
	// NetworkPolicy matches on IPs only, and resolving here would bake one
	// moment's answer into a static object that then goes stale silently.
	if len(p.GetEgressDomains()) > 0 {
		unsupported = append(unsupported, UnsupportedPolicyFeature{
			Feature: fmt.Sprintf("egress_domains=%v", p.GetEgressDomains()),
			Reason:  "NetworkPolicy matches IPs, not names, and a resolved snapshot would go stale",
		})
	}

	// Virtual-patch deny rules are deny-beats-allow (#660).
	if len(p.GetDenyRules()) > 0 {
		unsupported = append(unsupported, UnsupportedPolicyFeature{
			Feature: fmt.Sprintf("deny_rules (%d)", len(p.GetDenyRules())),
			Reason:  "NetworkPolicy is allow-only and cannot express a deny that overrides an allow",
		})
	}

	var rules []networkingv1.NetworkPolicyEgressRule
	for _, cidr := range p.GetEgressCidrs() {
		peers, err := egressPeersFor(cidr)
		if err != nil {
			unsupported = append(unsupported, UnsupportedPolicyFeature{
				Feature: "egress_cidr=" + cidr, Reason: err.Error(),
			})
			continue
		}

		// The metadata carve-out IS expressible: IPBlock.Except subtracts
		// sub-ranges from an allow rule, which is exactly deny-beats-allow for
		// this one address. An earlier version refused here on the belief that
		// NetworkPolicy had no such mechanism; it does, and refusing rejected
		// the commonest policy tenants actually write — allow a wide range,
		// carve out metadata — leaving them on the DNS-only floor with no
		// outbound network at all.
		//
		// Except entries must fall inside the peer's CIDR. That holds by
		// construction: coversMetadataIP is true only when the CIDR contains
		// the metadata address.
		if !p.GetAllowMetadata() && coversMetadataIP(cidr) {
			peers = withMetadataExcepted(peers)
		}

		rules = append(rules, networkingv1.NetworkPolicyEgressRule{To: peers})
	}

	// Deterministic order so a reconcile does not rewrite the object every
	// pass and leave an unreadable diff.
	sort.SliceStable(rules, func(i, j int) bool {
		return peerCIDR(rules[i]) < peerCIDR(rules[j])
