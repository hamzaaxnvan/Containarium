package cmd

import (
	"bytes"
	"context"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type plannerCaptureServer struct {
	pb.UnimplementedCrewServiceServer
	mu  sync.Mutex
	got *pb.PlanTaskGraphRequest
}

func (s *plannerCaptureServer) PlanTaskGraph(_ context.Context, req *pb.PlanTaskGraphRequest) (*pb.PlanTaskGraphResponse, error) {
	s.mu.Lock()
	s.got = proto.Clone(req).(*pb.PlanTaskGraphRequest)
	s.mu.Unlock()
