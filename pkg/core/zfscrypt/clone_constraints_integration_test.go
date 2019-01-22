//go:build zfs

// What ZFS actually permits when cloning an ENCRYPTED snapshot (#1160c).
//
// The container-snapshots design defers clone to its own slice, saying the
// API "carries a real constraint: a ZFS clone must stay within its origin's
// encryptionroot, so a clone across tenants is not expressible", and that it
// is "worth confirming on the lane before designing the API".
//
// This file is that confirmation, and nothing else. It adds no production
// code and no clone primitive — it records what ZFS does, so #1160c is
// designed against demonstrated behaviour rather than against a plausible
// reading of the manual.
//
// # What the lane answered
//
// The design's premise is WRONG. ZFS does NOT confine a clone to its origin's
// encryptionroot: a clone of tenant A's snapshot can be created under tenant
// B's dataset. What ZFS refuses to do is re-key it — the clone's
// encryptionroot stays A's, so B cannot read it even holding B's key.
//
// That is the same rule #1335 found for Incus instance volumes (a clone
// inherits encryption from its ORIGIN, not its location), and it moves the
// problem rather than removing it: the isolation claim holds, but tenant A's
// data can come to rest inside tenant B's subtree, where B's lifecycle
// operations act on it. A cross-tenant clone must therefore be refused by the
// DAEMON; ZFS will not refuse it.
//
// Also established: cloning requires the origin's key loaded, and a clone
// pins its origin snapshot ("snapshot has dependent clones") — which is the
// error #1160a's delete path already reports.
//
// The reason to spend a lane test on this is specific and recent. #1199's
// original design was built on an untested assumption about what Incus would
// accept, the assumption was wrong, and the whole mechanism had to be
// redesigned mid-sprint (#1335). The cost of checking first is one test; the
// cost of not checking was a week.
//
//	sudo go test -tags=zfs ./pkg/core/zfscrypt/ -run Clone -v
package zfscrypt

import (
	"context"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/testsupport/zfspool"
)

// Question 1: can a clone land in a DIFFERENT tenant's encryptionroot?
//
// ANSWERED, and the design's premise was wrong. #1160c was scoped on the
// claim that "a ZFS clone must stay within its origin's encryptionroot, so a
// clone across tenants is not expressible". ZFS permits the clone. What it
// does NOT do is re-key it: the clone lands under tenant B's path while its
// encryptionroot remains tenant A's.
//
// This is the same rule #1335 found for Incus instance volumes — a clone
// inherits encryption from its ORIGIN, not from its location — and it is why
// per-tenant encryptionroots had to move above the level Incus manages.
//
// So the hazard is not the one the design feared. B's key does not open the
// clone (asserted below, because that is the security-relevant half). The
// hazard is placement: tenant A's data comes to rest inside tenant B's
// dataset tree, where B's lifecycle operations — offboarding above all
// (#1343) — will act on it.
func TestIntegrationClone_LandsInAnotherTenantButKeepsItsOriginsKey(t *testing.T) {
	zfspool.Require(t)
	ctx := context.Background()
	p := zfspool.New(t)
	m := NewManager(nil)

	dsA, keyA := p.Dataset("tenant-a"), testKey(t, 0xAA)
	dsB, keyB := p.Dataset("tenant-b"), testKey(t, 0xBB)
	if err := m.CreateEncrypted(ctx, dsA, keyA); err != nil {
		t.Fatalf("CreateEncrypted(A): %v", err)
	}
	if err := m.CreateEncrypted(ctx, dsB, keyB); err != nil {
		t.Fatalf("CreateEncrypted(B): %v", err)
	}

	snap, err := m.Snapshot(ctx, dsA, "src")
	if err != nil {
		t.Fatalf("Snapshot(A): %v", err)
	}

	// A's snapshot, cloned INTO B's subtree. ZFS allows this.
	target := dsB + "/from-a"
	if _, stderr, err := m.run.Run(ctx, nil, "clone", snap, target); err != nil {
		t.Fatalf("clone across encryptionroots failed: %v: %s\n\nIf ZFS has started refusing "+
			"this, the design's original constraint is correct after all and #1160c gets simpler "+
			"— rewrite this test to pin the refusal", err, strings.TrimSpace(stderr))
	}

	// THE fact: location changed, key did not.
	root, err := m.EncryptionRoot(ctx, target)
	if err != nil {
		t.Fatalf("EncryptionRoot(clone): %v", err)
	}
	if root != dsA {
		t.Fatalf("clone %s has encryptionroot %q, want the ORIGIN's %q — if a clone were re-keyed "+
			"to its destination, tenant B would gain a readable copy of tenant A's data and the "+
			"isolation claim behind #1199 would be false", target, root, dsA)
	}

	// The security half, asserted rather than inferred from the string above.
	// Take A's key away while B's stays loaded: if the clone were readable
	// under B's key, it would still be mountable now.
	zfspool.UnmountIfMounted(t, target)
	zfspool.UnmountIfMounted(t, dsA)
	if err := m.UnloadKey(ctx, dsA); err != nil {
		t.Fatalf("UnloadKey(A): %v — the assertion below would prove nothing", err)
	}
	if status, err := m.KeyStatus(ctx, dsB); err != nil || status != KeyAvailable {
		t.Fatalf("precondition: B's key is %q (err %v), want it still loaded", status, err)
	}
	if status, err := m.KeyStatus(ctx, target); err != nil || status != KeyUnavailable {
		t.Fatalf("the clone living under tenant B reports keystatus %q (err %v), want %q — B "+
			"holds a loaded key and must still not be able to read A's cloned data",
			status, err, KeyUnavailable)
	}

	t.Logf("FACT for #1160c: a clone CAN be placed under another tenant (%s), but keeps its "+
		"origin's encryptionroot (%s) and is unreadable when the origin's key is unloaded, even "+
		"with the destination tenant's key available.\n\n"+
		"Consequences the design must answer:\n"+
		"  - The stated constraint ('not expressible') is wrong; a cross-tenant clone is "+
		"expressible and must be refused by the DAEMON, not left to ZFS.\n"+
		"  - Offboarding (#1343) walks a tenant's own subtree: it would destroy a clone of "+
		"another tenant's data sitting under the departing tenant, and would miss one that the "+
		"departing tenant left under someone else.", target, root)
}

// Question 2: a clone WITHIN the same encryptionroot — the case #1160c would
// actually ship, since a container clone belongs to the same tenant.
//
// Asserted positively so the refusal above is known to be about the
// encryptionroot boundary rather than about cloning being broken in this
// environment. A test that only shows a failure cannot tell those apart.
func TestIntegrationClone_WithinTheSameEncryptionrootWorks(t *testing.T) {
	zfspool.Require(t)
	ctx := context.Background()
	p := zfspool.New(t)
	m := NewManager(nil)

	tenant := p.Dataset("tenant-a")
	if err := m.CreateEncrypted(ctx, tenant, testKey(t, 0xAA)); err != nil {
		t.Fatalf("CreateEncrypted: %v", err)
	}
	// The shape the daemon uses: containers live under the tenant root, which
	// is the encryptionroot they inherit from.
	src := tenant + "/containers-src"
	if _, _, err := m.run.Run(ctx, nil, "create", src); err != nil {
		t.Fatalf("create source dataset: %v", err)
	}

	snap, err := m.Snapshot(ctx, src, "base")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	clone := tenant + "/containers-clone"
	if _, stderr, err := m.run.Run(ctx, nil, "clone", snap, clone); err != nil {
		t.Fatalf("cloning within one encryptionroot failed: %v: %s — if this cannot work, "+
			"#1160c has no viable shape at all", err, strings.TrimSpace(stderr))
	}

	// The clone must inherit the tenant's encryptionroot, not acquire one of
	// its own: a per-clone encryptionroot would be a second key to manage and
	// to lose, for data that is already the tenant's.
	root, err := m.EncryptionRoot(ctx, clone)
	if err != nil {
		t.Fatalf("EncryptionRoot(clone): %v", err)
