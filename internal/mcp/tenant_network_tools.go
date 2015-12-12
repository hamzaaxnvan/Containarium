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
	}
	b, err := c.doRequest(method, path, body)
	if err != nil {
		return err
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, out)
}
func (c *Client) NetworkPolicies() (*pb.ListNetworkPoliciesResponse, error) {
	out := &pb.ListNetworkPoliciesResponse{}
	err := c.tenantRPC("GET", "/v1/network-policies", nil, out)
	return out, err
}
func (c *Client) NetworkPolicy(tenant string) (*pb.GetNetworkPolicyResponse, error) {
	out := &pb.GetNetworkPolicyResponse{}
	err := c.tenantRPC("GET", "/v1/network-policies/"+url.PathEscape(tenant), nil, out)
	return out, err
}
func (c *Client) PatchTenantAccess(req *pb.PatchTenantAccessRequest) (*pb.PatchTenantAccessResponse, error) {
	out := &pb.PatchTenantAccessResponse{}
	err := c.tenantRPC("POST", "/v1/network-policies/tenant-access", req, out)
	return out, err
}

// parsePatchTenantAccessArgs builds the proto request field by field from the
// tool arguments; the raw argument map is never marshalled into the request.
func parsePatchTenantAccessArgs(args map[string]interface{}) (*pb.PatchTenantAccessRequest, error) {
	raw, ok := args["changes"]
	if !ok {
		return nil, fmt.Errorf("changes is required")
	}
	list, ok := raw.([]interface{})
	if !ok || len(list) == 0 {
