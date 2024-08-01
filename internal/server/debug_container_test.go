package server

import (
	"errors"
	"testing"

	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/stretchr/testify/assert"
)

func TestDiagnose(t *testing.T) {
	type tc struct {
		name         string
		report       *pb.DebugContainerResponse
		wantCause    string
		wantActionCt int
		wantFirstHas string
	}
	cases := []tc{
		{
			name:         "missing container",
			report:       &pb.DebugContainerResponse{ContainerState: "missing"},
			wantCause:    "does not exist",
			wantActionCt: 2,
			wantFirstHas: "create the container",
		},
		{
			name:         "stopped container",
			report:       &pb.DebugContainerResponse{ContainerState: "stopped"},
			wantCause:    "stopped",
			wantActionCt: 2,
			wantFirstHas: "start",
		},
		{
			// Three actions since #1478: dry-run preview, the repair, then
			// delete+recreate LAST. The first must be non-destructive — the
			// container and its data are intact in this failure mode, so an
