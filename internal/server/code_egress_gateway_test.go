package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// codeEgressHarness serves CodingToolEgressPolicyService (and, to prove the two
// stay apart, NetworkPolicyService) over in-process gRPC and through the
// generated grpc-gateway mux. Every call runs as an admin.
type codeEgressHarness struct {
	grpc pb.CodingToolEgressPolicyServiceClient
	np   pb.NetworkPolicyServiceClient
	mux  *runtime.ServeMux
}

func newCodeEgressHarness(t *testing.T) *codeEgressHarness {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		return h(auth.ContextWithTestSubject(ctx, "root-admin", auth.RoleAdmin), req)
	}))
	ce, _ := newTestCodeEgressServer()
