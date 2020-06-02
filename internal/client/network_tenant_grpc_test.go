package client

import (
	"context"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"net"
	"testing"
)

type nativeGRPCFixture struct {
	pb.UnimplementedContainerServiceServer
	pb.UnimplementedNetworkServiceServer
	ready    bool
	received *pb.CreateContainerRequest
}

func (f *nativeGRPCFixture) GetNetworkTenantCapabilities(context.Context, *pb.GetNetworkTenantCapabilitiesRequest) (*pb.GetNetworkTenantCapabilitiesResponse, error) {
	return &pb.GetNetworkTenantCapabilitiesResponse{Contract: "native-network-tenant/v1", BackendScope: "local-lxc", NativeMembershipRead: true, SharedTenantCreate: f.ready}, nil
}
func (f *nativeGRPCFixture) CreateContainer(_ context.Context, req *pb.CreateContainerRequest) (*pb.CreateContainerResponse, error) {
	f.received = req
	return &pb.CreateContainerResponse{Container: &pb.Container{Name: "workload-container", NetworkTenant: "observed-team", NetworkTenantSource: pb.NetworkTenantSource_NETWORK_TENANT_SOURCE_NATIVE}}, nil
}
func TestNativeTenantGRPCClientAndUnsupportedProviderNoWrite(t *testing.T) {
	for _, ready := range []bool{true, false} {
		fixture := &nativeGRPCFixture{ready: ready}
		listener := bufconn.Listen(1 << 20)
		srv := grpc.NewServer()
		pb.RegisterContainerServiceServer(srv, fixture)
		pb.RegisterNetworkServiceServer(srv, fixture)
		go func() { _ = srv.Serve(listener) }()
		conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
		if err != nil {
			t.Fatal(err)
		}
		c := &GRPCClient{conn: conn, client: pb.NewContainerServiceClient(conn), networkClient: pb.NewNetworkServiceClient(conn)}
		out, err := c.CreateContainer("workload", "image", "1", "1GB", "1GB", nil, false, "", nil, 0, 0, false, "", "", GitSourceOpts{}, 0, 0, false, 0, "", EncryptionOpts{TenantID: "default"}, "", "", "", nil, "team-a")
		if ready {
			if err != nil || fixture.received.GetNetworkTenant() != "team-a" || fixture.received.GetTenantId() != "default" || out.Tenant != "observed-team" || out.NetworkTenantSource != incus.NetworkTenantSourceNative {
				t.Fatalf("typed gRPC create: %+v %v", out, err)
			}
		} else if err == nil || fixture.received != nil {
			t.Fatal("unsupported gRPC Provider received create")
		}
		_ = c.Close()
		srv.Stop()
	}
}
func TestNativeTenantWireCompatibilityWithBaseDescriptor(t *testing.T) {
	// Model exactly the pre-feature descriptor: keep every published field,
