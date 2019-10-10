package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/policyreadiness"
)

// #2483: policy-store readiness is grpc.health.v1 on the daemon's gRPC
// server, served by the grpc-gateway at policyreadiness.HTTPPath without a
// token, so `containarium doctor` on the host can read it. Same transport
// chain as the daemon: in-process listener, transport-identity interceptor.
func TestPolicyHealthEndpoint_ServedThroughGatewayWithoutToken(t *testing.T) {
	srv := grpc.NewServer(
		grpc.Creds(auth.NewServerTransportCredentials(nil)),
		grpc.ChainUnaryInterceptor(auth.TransportIdentityUnaryInterceptor()),
	)
	h := health.NewServer()
	healthpb.RegisterHealthServer(srv, h)
	for _, svc := range policyreadiness.Services() {
		h.SetServingStatus(svc, healthpb.HealthCheckResponse_NOT_SERVING)
	}
	lis := auth.NewInternalListener()
	go func() { _ = srv.Serve(lis) }()
