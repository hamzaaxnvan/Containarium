package server

import (
	"context"
	"errors"
	"testing"

	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// KEY_UNAVAILABLE on container status (#1202, first acceptance criterion).
//
// ZFS lets a snapshot be TAKEN while the key is unloaded — which is
// deliberate, because blocking on key-custody reachability would let a
// transient outage suppress the backup window (design resolved decision #3).
// What it will not let you do is read one back. So the condition has to be
// visible on status, before someone trips over it at restore time.
//
// The distinction the enum exists to preserve: "not encrypted" and "encrypted
// but currently unopenable" are different situations with different operator
// responses, and "we could not determine it" is a third.

func TestEncryptionStateFor(t *testing.T) {
	aRef := map[string]zfskey.KeyRef{
		"alice-container": {Scheme: zfskey.SchemeFile, URI: "/keys/alice.key",
			Metadata: map[string]string{"tenant": "alice"}},
	}

	t.Run("an unencrypted container reports NONE", func(t *testing.T) {
		z, p := newZFSFake(), &fakeKeyProvider{key: aKey(t)}
		h := testHooksWith(t, z, p, &fakeRefStore{refs: map[string]zfskey.KeyRef{}}, newFakePools())

		if got := h.encryptionStateFor(context.Background(), "bob-container"); got != pb.EncryptionState_ENCRYPTION_STATE_NONE {
			t.Errorf("state = %v, want NONE", got)
		}
	})

	t.Run("a loaded key reports UNLOCKED", func(t *testing.T) {
		z, p := newZFSFake(), &fakeKeyProvider{key: aKey(t)}
		z.stdout["get"] = "available"
		refs := &fakeRefStore{refs: aRef, pools: map[string]string{"alice-container": "containarium-tenant-alice"}}
		pools := newFakePools()
		pools.sources["containarium-tenant-alice"] = "tank/tenants/alice"
		h := testHooksWith(t, z, p, refs, pools)

		if got := h.encryptionStateFor(context.Background(), "alice-container"); got != pb.EncryptionState_ENCRYPTION_STATE_UNLOCKED {
			t.Errorf("state = %v, want UNLOCKED", got)
		}
	})

