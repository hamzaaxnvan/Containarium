package server

import (
	"context"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/capabilities"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func localResourceCrewServer() (*CrewServer, *ContainerServer) {
	containers := &ContainerServer{peerPool: NewPeerPool("local-test", "", nil, "test-pool")}
	containers.capabStore().Record(capabilities.HostFacts{CPUCores: 8, TotalMemoryBytes: 16 << 30, GPUAvailable: true})
	return NewCrewServer(&AgentSkillServer{recipes: &RecipeServer{containers: containers}}), containers
}

// A later ineligible node must reject the whole plan before any earlier node
// provisions a box, including graphs mixing constrained and legacy nodes.
func TestCrewGraph_ResourcesRejectBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*CrewServer, *ContainerServer, *pb.RunCrewRequest)
		want    string
	}{
		{"cpu", func(_ *CrewServer, _ *ContainerServer, r *pb.RunCrewRequest) {
			r.Graph.Nodes[1].Resources.MinCpuCores = 9
		}, "CPU"},
		{"memory", func(_ *CrewServer, _ *ContainerServer, r *pb.RunCrewRequest) {
			r.Graph.Nodes[1].Resources.MinMemoryBytes = 17 << 30
		}, "memory"},
		{"gpu", func(_ *CrewServer, c *ContainerServer, _ *pb.RunCrewRequest) {
			c.capabStore().Record(capabilities.HostFacts{CPUCores: 8, TotalMemoryBytes: 16 << 30, ReportedClass: "gpu"})
		}, "GPU"},
		{"unknown profile", func(_ *CrewServer, c *ContainerServer, _ *pb.RunCrewRequest) {
			c.capabilityStore = capabilities.NewStore()
		}, "profile"},
		{"remote", func(_ *CrewServer, _ *ContainerServer, r *pb.RunCrewRequest) { r.BackendId = "remote-test" }, "local"},
		{"different pool", func(_ *CrewServer, _ *ContainerServer, r *pb.RunCrewRequest) { r.Pool = "other-pool" }, "pool"},
		{"unavailable", func(s *CrewServer, _ *ContainerServer, _ *pb.RunCrewRequest) { s.agents = nil }, "unavailable"},
		{"unhealthy", func(_ *CrewServer, c *ContainerServer, _ *pb.RunCrewRequest) {
			c.localHealthCheckFn = func() bool { return false }
		}, "unhealthy"},
		{"unknown backend identity", func(_ *CrewServer, c *ContainerServer, _ *pb.RunCrewRequest) {
			c.peerPool = NewPeerPool("", "", nil, "test-pool")
		}, "identity"},
