package incus

import (
	"fmt"
	"net/http"

	"github.com/lxc/incus/v7/shared/api"
)

// Storage-pool operations for per-tenant encrypted pools (#1338).
//
// Per-tenant ZFS encryption puts each tenant's containers on their own Incus
// storage pool, sourced at that tenant's encrypted dataset. Incus creates an
// instance volume by cloning the image snapshot, and a ZFS clone inherits
// encryption from its origin rather than its location (#1335) — so the only
// way an instance can be encrypted is for the dataset it is cloned WITHIN to
// be encrypted. That dataset is the pool's source.
//
// See docs/architecture/per-tenant-encrypted-storage-pools.md.
//
// EnsureStorage, next door, provisions the ONE pool the daemon is configured
// with and picks its driver by probing the host. These are the other shape:
