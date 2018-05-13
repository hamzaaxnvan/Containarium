package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Tenant offboarding (#1343).
//
// Per-tenant encryption creates two durable resources — an Incus storage pool
// and the encrypted dataset it is sourced at — and nothing removed them. A
// tenant who leaves kept both forever, the dataset holding their
// key-encrypted data indefinitely.
//
// The ORDER is the whole issue. Destroying the dataset first leaves an Incus
// pool pointing at nothing, and `EnsureStorage`/`reviewExistingStorage` walk
// every pool at daemon start — so a mis-ordered teardown breaks startup for
// every tenant on the host, not just the departing one.

func offboardHooks(t *testing.T, z *zfsFake, containers map[string]zfskey.KeyRef) (*encryptionHooks, *fakePools, *fakeRefStore) {
	t.Helper()
	// The shared fake defaults `zfs list` to failing, which means "dataset
	// absent" for Exists. Offboarding asks the same verb a different
	// question — "does this root have children?" — so the tests here supply
	// a real listing instead.
	delete(z.errs, "list")
	pools := newFakePools()
	pools.sources["containarium-tenant-alice"] = "tank/tenants/alice"
	placed := map[string]string{}
	for name := range containers {
		placed[name] = "containarium-tenant-alice"
	}
	refs := &fakeRefStore{refs: containers, pools: placed}
	return testHooksWith(t, z, &fakeKeyProvider{key: aKey(t)}, refs, pools), pools, refs
}

// The guard: a tenant with containers still on their pool is not offboarded.
// Deleting the pool underneath a live container destroys its storage.
func TestDestroyTenantStorage_RefusesWhileContainersRemain(t *testing.T) {
	z := newZFSFake()
	// ZFS reports a container dataset still living under the tenant root.
	z.stdout["list"] = "tank/tenants/alice\ntank/tenants/alice/containers/alice-container"
	h, pools, _ := offboardHooks(t, z, map[string]zfskey.KeyRef{})

	err := h.DestroyTenantStorage(context.Background(), "alice")
	if err == nil {
		t.Fatal("offboarded a tenant that still has containers — their storage would be deleted " +
			"out from under a live box")
	}
	if !strings.Contains(err.Error(), "tank/tenants/alice") {
		t.Errorf("the error does not name what is still there, so an operator cannot act: %v", err)
	}
	if len(pools.deleted) != 0 {
		t.Errorf("the pool was deleted anyway: %v", pools.deleted)
