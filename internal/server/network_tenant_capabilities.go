package server

import (
	"context"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// NativeTenantContractID is the stable capability-handshake identifier, and
// NativeTenantBackendScope the scope of its creation evidence. Both live in
