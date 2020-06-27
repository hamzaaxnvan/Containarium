package codeegress

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Client side of CodingToolEgressPolicyService, shared by
// `containarium code egress-policy` and the MCP code_egress_policy tool so
// both read and print through the same functions.

// PolicyPath is the REST path for a tenant's policy, or the cluster default
// when tenant is empty — the (google.api.http) mapping in code_egress.proto.
func PolicyPath(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "/v1/code/egress-policy"
	}
	return "/v1/code/egress-policy/" + url.PathEscape(tenant)
}

// Reader is the Get RPC; the gRPC, HTTP and MCP clients all satisfy it.
type Reader interface {
	GetCodingToolEgressPolicy(req *pb.GetCodingToolEgressPolicyRequest) (*pb.GetCodingToolEgressPolicyResponse, error)
}

// Writer is the Set RPC.
type Writer interface {
	SetCodingToolEgressPolicy(req *pb.SetCodingToolEgressPolicyRequest) (*pb.SetCodingToolEgressPolicyResponse, error)
}

// Get reads the stored and effective policy for tenant ("" = cluster default).
func Get(r Reader, tenant string) (*pb.GetCodingToolEgressPolicyResponse, error) {
	tenant = strings.TrimSpace(tenant)
	resp, err := r.GetCodingToolEgressPolicy(&pb.GetCodingToolEgressPolicyRequest{Tenant: tenant})
	if err != nil {
		return nil, fmt.Errorf("get coding-tool egress policy for %s: %w", displayTenant(tenant), err)
	}
	return resp, nil
}

// Set replaces the policy for tenant ("" = cluster default).
func Set(w Writer, tenant string, mode pb.NetworkPolicyMode, cidrs, domains []string) (*pb.CodingToolEgressPolicy, error) {
	tenant = strings.TrimSpace(tenant)
	resp, err := w.SetCodingToolEgressPolicy(&pb.SetCodingToolEgressPolicyRequest{Policy: &pb.CodingToolEgressPolicy{
		Tenant: tenant, Mode: mode, EgressCidrs: cidrs, EgressDomains: domains,
	}})
	if err != nil {
		return nil, fmt.Errorf("set coding-tool egress policy for %s: %w", displayTenant(tenant), err)
	}
	return resp.GetPolicy(), nil
}

// ParseMode maps a typed flag value (log_only | enforce, or the enum name) to
// the enum. There is no default: the mode is an explicit choice.
func ParseMode(s string) (pb.NetworkPolicyMode, error) {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
	norm = strings.TrimPrefix(norm, "network_policy_mode_")
	switch norm {
	case "log_only":
		return pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY, nil
	case "enforce":
		return pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE, nil
	}
	return 0, fmt.Errorf("unknown mode %q (want log_only or enforce)", s)
}

// WriteResponse prints a Get result for a human or an agent.
func WriteResponse(w io.Writer, resp *pb.GetCodingToolEgressPolicyResponse) error {
	eff := resp.GetEffective()
	var b strings.Builder
	fmt.Fprintf(&b, "tenant:     %s\n", displayTenant(eff.GetTenant()))
	if p := resp.GetPolicy(); p != nil {
		fmt.Fprintf(&b, "stored:     revision %d by %s", p.GetRevision(), p.GetUpdatedBy())
		if t := p.GetUpdatedAt(); t != nil {
			fmt.Fprintf(&b, " at %s", t.AsTime().UTC().Format("2006-01-02T15:04:05Z"))
		}
		b.WriteString("\n")
	}
	if !eff.GetRestricted() {
		b.WriteString("effective:  unrestricted (no policy for this tenant and no cluster default)\n")
		_, err := io.WriteString(w, b.String())
		return err
