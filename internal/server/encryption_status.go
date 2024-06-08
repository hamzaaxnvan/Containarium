package server

import (
	"context"
	"log"

	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Encryption state on container status (#1202), design resolved decision #3.
//
// ZFS allows a snapshot to be TAKEN while the key is unloaded and refuses
