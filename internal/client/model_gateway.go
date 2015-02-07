package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// ModelGatewayService client, both transports (#1726).
//
// The REST paths here are not a second source of truth: they are the
// (google.api.http) mapping in proto/containarium/v1/model_gateway.proto, and
// http_model_gateway_route_test.go pins every one of them against it. A path
// changed in the proto and not here is a failing test, not a runtime 404.
//
// Note the {provider} path segment carries the ENUM'S NAME
// ("GATEWAY_PROVIDER_KAFEIDO"), because that is what grpc-gateway's runtime.Enum
// converter parses — the enum is the contract on the wire too, not a lowercase
// nickname that the daemon would have to re-map.

// modelGatewayTimeout bounds one ModelGatewayService call. The model-list verb
// reaches an upstream provider, so this has to cover a round trip beyond the
// daemon; the gateway's own ListModels deadline is shorter and fires first.
const modelGatewayTimeout = 30 * time.Second

// ModelGatewayAPI is the transport-independent view of ModelGatewayService, so a
// CLI verb can be written once against either transport.
type ModelGatewayAPI interface {
	SetTenantProviderKey(req *pb.SetTenantProviderKeyRequest) (*pb.SetTenantProviderKeyResponse, error)
	DeleteTenantProviderKey(req *pb.DeleteTenantProviderKeyRequest) (*pb.DeleteTenantProviderKeyResponse, error)
	GetTenantProviderKeyStatus(req *pb.GetTenantProviderKeyStatusRequest) (*pb.GetTenantProviderKeyStatusResponse, error)
	MintGatewayToken(req *pb.MintGatewayTokenRequest) (*pb.MintGatewayTokenResponse, error)
	ListGatewayModels(req *pb.ListGatewayModelsRequest) (*pb.ListGatewayModelsResponse, error)
	Close() error
}

// gatewayKeyPath is the REST path for one (key owner, provider) key. The owner is
// escaped because it carries a ':' separator by construction (`org:<id>`), and
// the provider is the enum's name.
func gatewayKeyPath(keyOwner string, provider pb.GatewayProvider) string {
	return "/v1/model-gateway/keys/" + url.PathEscape(keyOwner) + "/" + url.PathEscape(provider.String())
}

// ---------- gRPC ----------

// SetTenantProviderKey stores or rotates one key owner's real upstream provider
// key. Requires the gateway:admin scope.
func (c *GRPCClient) SetTenantProviderKey(req *pb.SetTenantProviderKeyRequest) (*pb.SetTenantProviderKeyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelGatewayTimeout)
	defer cancel()
	resp, err := c.modelGatewayClient.SetTenantProviderKey(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("set tenant provider key: %w", err)
	}
	return resp, nil
}

// DeleteTenantProviderKey removes one key owner's provider key and revokes the
// tokens issued against it. Requires the gateway:admin scope.
func (c *GRPCClient) DeleteTenantProviderKey(req *pb.DeleteTenantProviderKeyRequest) (*pb.DeleteTenantProviderKeyResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelGatewayTimeout)
	defer cancel()
	resp, err := c.modelGatewayClient.DeleteTenantProviderKey(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("delete tenant provider key: %w", err)
	}
	return resp, nil
}

// GetTenantProviderKeyStatus reports a key's fingerprint and set_at — never the
// key. Requires the gateway:admin scope.
func (c *GRPCClient) GetTenantProviderKeyStatus(req *pb.GetTenantProviderKeyStatusRequest) (*pb.GetTenantProviderKeyStatusResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelGatewayTimeout)
	defer cancel()
	resp, err := c.modelGatewayClient.GetTenantProviderKeyStatus(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get tenant provider key status: %w", err)
	}
	return resp, nil
}

