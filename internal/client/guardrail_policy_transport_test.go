package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	"github.com/footprintai/containarium/internal/server"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The guardrail policy contract over both transports (#2368): the REST
// PUT/GET /v1/guardrail/policy through the generated grpc-gateway shim and
// the gRPC calls reach the same real GuardrailPolicyServer and agree.

type guardrailPolicyClient interface {
	GetGuardrailPolicy() (*pb.GetGuardrailPolicyResponse, error)
	SetGuardrailPolicy(req *pb.SetGuardrailPolicyRequest) (*pb.SetGuardrailPolicyResponse, error)
}

func gpClaims(admin bool) *auth.Claims {
	if admin {
		return &auth.Claims{Username: "ops", Roles: []string{auth.RoleAdmin}}
	}
	return &auth.Claims{Username: "data-owner", Roles: []string{"user"}}
}

func startGRPCGuardrailPolicy(t *testing.T, srv *server.GuardrailPolicyServer, admin bool) *GRPCClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		return h(auth.ContextWithClaims(ctx, gpClaims(admin)), req)
	}))
	pb.RegisterGuardrailPolicyServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	c, err := NewGRPCClient(lis.Addr().String(), "", true)
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// startGatewayGuardrailPolicy mirrors the daemon gateway's JSON options and
// error body (internal/gateway/gateway.go), like startGatewayAgentServer.
func startGatewayGuardrailPolicy(t *testing.T, srv *server.GuardrailPolicyServer, admin bool) *HTTPClient {
	t.Helper()
	mux := runtime.NewServeMux(
		runtime.WithErrorHandler(func(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler, w http.ResponseWriter, _ *http.Request, err error) {
			code := runtime.HTTPStatusFromCode(status.Code(err))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "code": code})
		}),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions:   protojson.MarshalOptions{EmitUnpopulated: true},
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
		}),
	)
	if err := pb.RegisterGuardrailPolicyServiceHandlerServer(context.Background(), mux, srv); err != nil {
		t.Fatalf("register gateway handler: %v", err)
	}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(auth.ContextWithClaims(r.Context(), gpClaims(admin))))
	}))
	t.Cleanup(hs.Close)
	c, err := NewHTTPClient(hs.URL, "tok")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return c
}

// restSetStatus PUTs req to the gateway behind c and returns the HTTP status,
// so a refusal is asserted by its status rather than by error text.
func restSetStatus(t *testing.T, c *HTTPClient, req *pb.SetGuardrailPolicyRequest) int {
	t.Helper()
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
