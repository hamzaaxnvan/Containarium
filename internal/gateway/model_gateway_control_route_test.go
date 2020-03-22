package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ModelGatewayService's REST mapping, end to end through the real grpc-gateway
// mux (#1726).
//
// internal/client/http_model_gateway_route_test.go pins the paths the CLIENT
// builds; this pins that the generated .pb.gw.go patterns actually MATCH those
// paths. Both halves are needed: a client path and a server pattern can each be
// self-consistent and still not meet.
//
// A 501 means the mux routed the request into the (Unimplemented) handler — which
// is the assertion. A 404 means no pattern matched, i.e. drift.
func TestModelGatewayControlRoutes(t *testing.T) {
	srv := grpc.NewServer(
		grpc.Creds(auth.NewServerTransportCredentials(nil)),
		grpc.ChainUnaryInterceptor(auth.TransportIdentityUnaryInterceptor()),
	)
	pb.RegisterModelGatewayServiceServer(srv, pb.UnimplementedModelGatewayServiceServer{})
	lis := auth.NewInternalListener()
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
