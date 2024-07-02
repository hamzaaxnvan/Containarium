package cmd

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium crew run --engine` flag -> RunCrewRequest.engine_overrides
// wiring (#2228), mirroring agent_run_test.go's TestAgentRun_FlagsReachDaemonRequest
// pattern for `agent run --engine`.

func resetCrewRunFlags() {
	crewRunGraphFile = ""
	crewRunConcurrency = 0
	crewRunBackendID = ""
	crewRunPool = ""
	crewRunInput = ""
	crewRunGitSource = ""
	crewRunGitRef = ""
	crewRunGitCredentialFile = ""
	crewRunEngineOverrides = nil
	crewRunModelOverrides = nil
}

func TestCrewRun_GraphFlagsReachDaemonRequest(t *testing.T) {
	for _, transport := range []string{"grpc", "http"} {
		t.Run(transport, func(t *testing.T) {
			resetCrewRunFlags()
			t.Cleanup(resetCrewRunFlags)
			capture := &crewRunCaptureServer{}
			startCrewRunDaemon(t, transport, capture)
			plan := strings.Replace(examplePlanYAML, "    skill_id: issue-implementer", "    skill_id: issue-implementer\n    resources:\n      min_cpu_cores: 4\n      min_memory_bytes: 8589934592\n      requires_gpu: true", 1)
			crewRunGraphFile = writePlan(t, "plan.yaml", plan)
			crewRunConcurrency = 2
			if err := runCrewRun(crewRunCmd, []string{"freeform-crew"}); err != nil {
				t.Fatal(err)
			}
			capture.mu.Lock()
			got := capture.got
			capture.mu.Unlock()
			graph, err := loadTaskGraph(crewRunGraphFile)
			if err != nil {
				t.Fatal(err)
			}
			if got.CrewId != "freeform-crew" || got.Concurrency != 2 || !proto.Equal(got.Graph, graph) {
				t.Fatalf("graph request did not survive %s transport: %v", transport, got)
			}
			r := got.Graph.Nodes[0].GetResources()
			if r.GetMinCpuCores() != 4 || r.GetMinMemoryBytes() != 8<<30 || !r.GetRequiresGpu() {
				t.Fatalf("resource requirements lost over %s: %v", transport, r)
			}
		})
	}
}

// crewRunCaptureServer records the RunCrew request the daemon's handler
// receives.
type crewRunCaptureServer struct {
	pb.UnimplementedCrewServiceServer

	mu  sync.Mutex
	got *pb.RunCrewRequest
}

func (s *crewRunCaptureServer) RunCrew(_ context.Context, req *pb.RunCrewRequest) (*pb.RunCrewResponse, error) {
	s.mu.Lock()
	s.got = proto.Clone(req).(*pb.RunCrewRequest)
	s.mu.Unlock()
	return &pb.RunCrewResponse{Run: &pb.CrewRun{Id: "run-1", CrewId: req.GetCrewId()}}, nil
}

func startCrewRunDaemon(t *testing.T, transport string, capture pb.CrewServiceServer) {
	t.Helper()
	prevServer, prevHTTP, prevToken, prevInsecure := serverAddr, httpMode, authToken, insecure
	t.Cleanup(func() { serverAddr, httpMode, authToken, insecure = prevServer, prevHTTP, prevToken, prevInsecure })

	switch transport {
	case "grpc":
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
