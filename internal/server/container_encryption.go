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

// defaultTenantID is the only tenant a single-tenant daemon recognises.
// Callers may also leave the field unset.
const defaultTenantID = "default"

// errEncryptionNotWired is the refusal for an encrypted create on a daemon
// that has key custody but no encryption storage wired. Shared by the real
// create and its dry run (#2925) so the two cannot say different things.
var errEncryptionNotWired = status.Error(codes.FailedPrecondition,
	"encrypted=true resolved no storage pool for this tenant: the daemon has key "+
		"custody configured but no encryption storage wired, so the container would "+
		"be created on the default pool without encryption. Refusing rather than "+
		"delivering plaintext under an encryption guarantee")

// validateEncryption rejects an encrypted create when the daemon has no
// key custody wired.
//
// Failing closed is the whole point: silently provisioning an
// unencrypted dataset would hand the caller a guarantee they do not
