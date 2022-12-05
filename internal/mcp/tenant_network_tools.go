package mcp

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// tenantNetworkAPI is the slice of the MCP backend the Tenant-network tools
// need. The Host MCP client implements it over REST with typed protobuf
// request/response values; no handler builds a request by re-marshalling raw
// tool arguments.
type tenantNetworkAPI interface {
	NetworkPolicies() (*pb.ListNetworkPoliciesResponse, error)
	NetworkPolicy(string) (*pb.GetNetworkPolicyResponse, error)
	PatchTenantAccess(*pb.PatchTenantAccessRequest) (*pb.PatchTenantAccessResponse, error)
}

// tenantRPC performs one REST round trip with a typed request and response.
func (c *Client) tenantRPC(method, path string, req proto.Message, out proto.Message) error {
	var body interface{}
	if req != nil {
		b, err := protojson.Marshal(req)
		if err != nil {
			return err
		}
		body = b
