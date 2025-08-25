// Package codeegress holds the pure half of the coding tool's egress
// allowlist (#2370, docs/architecture/coding-cli-egress-allowlist.md):
// validating a CodingToolEgressPolicy and resolving the effective policy a
// run takes. No store, no kernel, no network.
package codeegress

import (
	"errors"
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// validationTenant stands in for the cluster default's empty tenant when the
// lists are checked with netpolicy.Compile, which requires a tenant. Only the
// CIDR, domain and mode rules are taken from it.
const validationTenant = "coding-tool-egress-default"

// Implicit is what the daemon knows about the always-allowed destinations.
// GatewayEndpoint is the host:port a box reaches the model gateway on; empty
// when the daemon cannot name it.
type Implicit struct {
	GatewayEndpoint string
}

// Normalize validates a policy with the same rules as NetworkPolicy and
// returns its stored form: masked, deduped, sorted CIDRs; normalized domains.
// Unlike NetworkPolicy, the mode has no default: UNSPECIFIED (or an unknown
// value) is an error, so a caller who forgets it cannot store a policy that
// drops nothing. Server-assigned fields (revision, updated_at, updated_by)
// are dropped; the store sets them.
func Normalize(p *pb.CodingToolEgressPolicy) (*pb.CodingToolEgressPolicy, error) {
	if p == nil {
		return nil, errors.New("coding-tool egress policy is required")
	}
	switch p.GetMode() {
	case pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY, pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE:
	default:
		return nil, fmt.Errorf("mode must be LOG_ONLY or ENFORCE, got %v: a coding-tool egress policy has no default mode", p.GetMode())
	}
	tenant := strings.TrimSpace(p.GetTenant())
	check := tenant
	if check == "" {
		check = validationTenant
	}
	compiled, err := netpolicy.Compile(&pb.NetworkPolicy{
		Tenant:        check,
		EgressCidrs:   p.GetEgressCidrs(),
		EgressDomains: p.GetEgressDomains(),
		Mode:          p.GetMode(),
	})
	if err != nil {
		return nil, err
	}
