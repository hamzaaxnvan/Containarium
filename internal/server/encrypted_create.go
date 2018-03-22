package server

import (
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Wiring for the encrypted create path (#1341), per
// docs/architecture/per-tenant-encrypted-storage-pools.md.

// zfsKeyCacheTTL bounds how long a tenant's key stays resident after its last
// use. Long enough that a stop/start cycle does not re-hit key custody, short
// enough that an idle tenant's key does not sit in daemon memory forever.
const zfsKeyCacheTTL = 30 * time.Minute

// encryptionTenantFor decides whose key a container is encrypted with.
//
// On this build the username IS the tenant: validateTenantID rejects any
// tenant_id other than unset or "default", so there is no other tenancy to
// scope a key by — and scoping by "default" would put every user of the
// daemon under one encryptionroot. No isolation at all, and silently.
//
// A real tenant_id wins where one exists (the cloud build's org-scoped
// tenancy), because an org's several users must share one encryptionroot or
// they cannot share datasets.
func encryptionTenantFor(req *pb.CreateContainerRequest) string {
	if id := req.GetTenantId(); id != "" && id != defaultTenantID {
		return id
	}
	return req.GetUsername()
}

// recordEncryptedPlacement stores the key ref and storage pool on a
// just-created container, and removes the container if it cannot.
//
// Deleting is the right failure mode, not tidiness. The container's data is
// already encrypted under the tenant's key; without a recorded placement
// nothing can name its encryptionroot, so it can never be started and never
// safely reused — and its name would block the next create. Leaving one
// behind is worse than failing the create.
//
