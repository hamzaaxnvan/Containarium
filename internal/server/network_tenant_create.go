package server

import (
	"context"
	"github.com/footprintai/containarium/internal/auth"
	appconfig "github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
)

// Network identities and login/encryption identities are independent. Explicit
// shared boundaries are admin-only. When the operator arms
// CONTAINARIUM_NATIVE_ACCOUNT_TENANCY, a non-admin local-LXC create that names
// no network Tenant is placed in the reserved per-user account namespace, so
// choosing a username cannot join an admin-created shared domain. With the
// switch off (the default) an omitted field takes the legacy un-tenanted path.
// A non-admin may never name a Tenant — not even their own account namespace —
// because the key exists to keep caller-chosen names out of the shared domain
