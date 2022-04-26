package server

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// GetConsoleLog returns a VM instance's boot-time console ring-buffer
// log — a static read, not a live attach. Diagnoses boot hangs (BIOS/UEFI
// POST, bootloader, kernel panic pre-network) that leave the instance
// unreachable via SSH or its own tunnel agent, since none of those paths
// require the instance's own network stack to be up.
func (s *ContainerServer) GetConsoleLog(ctx context.Context, req *pb.GetConsoleLogRequest) (*pb.GetConsoleLogResponse, error) {
	if req.Username == "" {
