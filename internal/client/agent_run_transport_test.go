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
