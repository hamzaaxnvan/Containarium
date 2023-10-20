//go:build !containarium_client

package server

// Server-side caps (#1417): the operator's hard ceiling on what any
// tenant may configure a cluster to consume. Refusals are typed errors
// (and REFUSED events on updates) — never silent clamps.

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func group(name, cpu, mem, disk string, min, max int32) *pb.NodeGroup {
	return &pb.NodeGroup{
		Name: name, Size: &pb.ResourceLimits{Cpu: cpu, Memory: mem, Disk: disk},
		MinNodes: min, MaxNodes: max,
	}
}

func TestClusterCaps_ParseEnv(t *testing.T) {
	caps, err := parseClusterCaps("10", "cpu=8,memory=16GB,disk=200GB")
	if err != nil {
		t.Fatalf("parseClusterCaps: %v", err)
	}
	if caps.maxNodes != 10 || caps.maxCPU != 8 || caps.maxMemoryBytes != 16_000_000_000 || caps.maxDiskBytes != 200_000_000_000 {
		t.Fatalf("caps = %+v", caps)
