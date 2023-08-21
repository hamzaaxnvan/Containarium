package server

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Request validation for per-tenant dataset encryption (#1198), phase 2
// of docs/ZFS-PER-CONTAINER-ENCRYPTION-DESIGN.md.
//
// The flag is accepted and validated here; the lifecycle hooks that
// actually create an encrypted dataset land in #1199/#1201. Until then a
// daemon with no KeyProvider — which is every OSS daemon today — rejects
// an encrypted create outright, so there is no window in which the API
// accepts "encrypt this" and produces plaintext.
