package cmd

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium agent run` flag -> RunAgentSkillRequest wiring (#2042).

// resetAgentRunFlags zeroes the package-level flag vars so each table case
// starts from the command's defaults rather than the previous case's values.
func resetAgentRunFlags() {
	agentRunBackendID = ""
	agentRunPool = ""
	agentRunInput = ""
	agentRunGitSource = ""
	agentRunGitRef = ""
	agentRunGitCredentialFile = ""
	agentRunTrackerConnection = ""
	agentRunEngine = ""
}

func TestAgentRun_TrackerConnectionFlagRegistered(t *testing.T) {
	f := agentRunCmd.Flags().Lookup("tracker-connection")
	if f == nil {
		t.Fatal("`agent run` has no --tracker-connection flag")
	}
	if f.DefValue != "" {
		t.Errorf("--tracker-connection default = %q, want empty (no binding)", f.DefValue)
	}
}

// agentRunCaptureServer records the RunAgentSkill request the daemon's
// handler receives.
type agentRunCaptureServer struct {
	pb.UnimplementedAgentSkillServiceServer

	mu  sync.Mutex
	got *pb.RunAgentSkillRequest
}

func (s *agentRunCaptureServer) RunAgentSkill(_ context.Context, req *pb.RunAgentSkillRequest) (*pb.RunAgentSkillResponse, error) {
	s.mu.Lock()
	s.got = proto.Clone(req).(*pb.RunAgentSkillRequest)
	s.mu.Unlock()
	return &pb.RunAgentSkillResponse{Container: &pb.Container{Name: req.GetSkillId() + "-box"}}, nil
}

// startAgentRunDaemon serves capture the way the daemon does and points the
// CLI's global connection flags at it: over gRPC on a loopback port
// (--insecure), or over HTTP through the generated grpc-gateway handler
// (--http) with unknown JSON fields rejected, as the daemon's gateway does.
func startAgentRunDaemon(t *testing.T, transport string, capture *agentRunCaptureServer) {
	t.Helper()
	prevServer, prevHTTP, prevToken, prevInsecure := serverAddr, httpMode, authToken, insecure
	t.Cleanup(func() { serverAddr, httpMode, authToken, insecure = prevServer, prevHTTP, prevToken, prevInsecure })

	switch transport {
	case "grpc":
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		srv := grpc.NewServer()
		pb.RegisterAgentSkillServiceServer(srv, capture)
		go func() { _ = srv.Serve(lis) }()
		t.Cleanup(srv.Stop)
		serverAddr, httpMode, authToken, insecure = lis.Addr().String(), false, "", true
	case "http":
		mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
		}))
		if err := pb.RegisterAgentSkillServiceHandlerServer(context.Background(), mux, capture); err != nil {
			t.Fatalf("register gateway handler: %v", err)
		}
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		serverAddr, httpMode, authToken, insecure = srv.URL, true, "tok", false
	default:
		t.Fatalf("unknown transport %q", transport)
	}
}

// TestAgentRun_FlagsReachDaemonRequest runs `agent run` against a daemon
