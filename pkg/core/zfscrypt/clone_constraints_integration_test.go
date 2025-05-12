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
