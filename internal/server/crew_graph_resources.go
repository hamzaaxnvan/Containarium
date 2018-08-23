package server

import (
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validateLocalNodeResources returns the local backend to pin a constrained
// node to. Empty means legacy placement. This checks the last measured host
// profile, not free capacity: normal provisioning/admission still applies.
func (s *CrewServer) validateLocalNodeResources(node *pb.TaskNode, parent *pb.RunCrewRequest) (string, error) {
