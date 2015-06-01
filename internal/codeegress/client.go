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
