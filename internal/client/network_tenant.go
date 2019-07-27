package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// networkTenantSourceFromProto maps the wire enum to the provider-source
// string. The UNSPECIFIED zero value becomes "" so an old daemon (which never
// sets the field) and the HTTP client (which omits absent fields) agree, and
// `containarium get` does not print a tenant line for every un-tenanted box.
func networkTenantSourceFromProto(s pb.NetworkTenantSource) incus.NetworkTenantSource {
	if s == pb.NetworkTenantSource_NETWORK_TENANT_SOURCE_UNSPECIFIED {
		return ""
	}
	return incus.NetworkTenantSource(s.String())
}

func (c *GRPCClient) GetNetworkTenantCapabilities() (*pb.GetNetworkTenantCapabilitiesResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.networkClient.GetNetworkTenantCapabilities(ctx, &pb.GetNetworkTenantCapabilitiesRequest{})
}
func (c *HTTPClient) GetNetworkTenantCapabilities() (*pb.GetNetworkTenantCapabilitiesResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.doRequest(ctx, http.MethodGet, "/v1/network-tenants/capabilities", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("network Tenant capability status %d", resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := &pb.GetNetworkTenantCapabilitiesResponse{}
	// DiscardUnknown: a newer daemon may add capability fields; the handshake
	// exists to tolerate newer providers, not to refuse every explicit create
	// until this client is rebuilt.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, out); err != nil {
		return nil, err
	}
