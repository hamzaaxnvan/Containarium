package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// RunAgentSkill end to end over both transports (#2042): the values the CLI
// passes must reach the daemon's AgentSkillService handler intact, including
// tracker_connection, whether they travel as gRPC or as JSON through the
// grpc-gateway REST shim.

// fakeAgentSkillServer records the RunAgentSkill request it receives and
// either answers with a fixed response or fails with err.
type fakeAgentSkillServer struct {
	pb.UnimplementedAgentSkillServiceServer

	mu  sync.Mutex
	got *pb.RunAgentSkillRequest
	err error
}

func (f *fakeAgentSkillServer) RunAgentSkill(_ context.Context, req *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
	f.mu.Lock()
	f.got = proto.Clone(req).(*pb.RunAgentSkillRequest)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &pb.RunAgentSkillResponse{
		Container:    &pb.Container{Name: req.GetSkillId() + "-box"},
		ArtifactJson: `{"ok":true}`,
		RunId:        "run-1",
	}, nil
}

func (f *fakeAgentSkillServer) received() *pb.RunAgentSkillRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got
}

// startGRPCAgentServer serves fake on a loopback port and returns a client
// built by the real NewGRPCClient constructor.
func startGRPCAgentServer(t *testing.T, fake *fakeAgentSkillServer) *GRPCClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterAgentSkillServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	c, err := NewGRPCClient(lis.Addr().String(), "", true)
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// startGatewayAgentServer serves fake behind the generated grpc-gateway
// handler, with the same JSON options and error body as the daemon's gateway
// (internal/gateway/gateway.go): unknown request fields are rejected, so a
// misspelled key fails here instead of being silently dropped, and errors
// come back as {"error": ..., "code": ...} like customErrorHandler writes.
func startGatewayAgentServer(t *testing.T, fake *fakeAgentSkillServer) *HTTPClient {
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
	if err := pb.RegisterAgentSkillServiceHandlerServer(context.Background(), mux, fake); err != nil {
		t.Fatalf("register gateway handler: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return c
}

// agentRunner is the method under test, shared by both clients.
type agentRunner interface {
	RunAgentSkill(skillID, backendID, pool, inputJSON, gitSource, gitRef, gitCredential, trackerConnection string, engine pb.AgentEngine) (*pb.RunAgentSkillResponse, error)
}

// runAgentSkill calls c with req's fields in RunAgentSkill's positional order.
func runAgentSkill(c agentRunner, req *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
	return c.RunAgentSkill(req.GetSkillId(), req.GetBackendId(), req.GetPool(), req.GetInputJson(),
		req.GetGitSource(), req.GetGitRef(), req.GetGitCredential(), req.GetTrackerConnection(), req.GetEngine())
}

var agentRunTransports = []struct {
	name  string
	start func(*testing.T, *fakeAgentSkillServer) agentRunner
