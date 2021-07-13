package server

import (
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// backendStorageFromPool projects an observed incus pool driver onto the wire
// contract, so an operator can answer "which of my backends can have one
// tenant stall another's fsync?" (#1206) from ListBackends rather than by
// reading a daemon startup log on each host. See #1209.
//
