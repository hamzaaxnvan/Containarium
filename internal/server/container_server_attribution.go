package server

import (
	"context"
	"log"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetContainerAttribution merges labels onto an EXISTING container's config
// (cloud #746). The hosted control plane stamps attribution labels
// (cloud_org_id / cloud_container_id / managed_by) at CreateContainer today;
// this lets it stamp them AFTER the fact, which the cloud's adopt flow (cloud
// #539) needs to bring a host's pre-existing (orphan) container under org
// management.
//
// Merge, not replace: each provided label is set via manager.AddLabel (which
