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
