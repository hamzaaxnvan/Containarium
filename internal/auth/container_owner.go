package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Phase 1.4 follow-up — container-name → owner derivation
// for handlers whose authz key is a container name (not a
// username).
//
// Tenant container names follow the convention
//
//	<username>-container
//
// established by ContainerServer.CreateContainer
// (`req.Username + "-container"`). The convention is enforced
// at create time and stable across the codebase. Several RPC
// handlers (TrafficServer.*, security_server.ClamAV-tenant
// reads) take `container_name` rather than `username`, so we
// can't call AuthorizeTenant directly — we need to derive the
// owner first.
//
// System containers (core services like Caddy, VictoriaMetrics)
// don't follow the convention. Calls naming a system container
