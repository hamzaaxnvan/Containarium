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
