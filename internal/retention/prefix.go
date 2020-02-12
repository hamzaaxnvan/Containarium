package retention

import (
	"context"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// PrefixSource is a store in which several classes share ONE ordered history
// that can only be removed from the front: the hash-chained audit_logs (#2878).
// Deleting a row in the middle would break verification of every later row, so
// what a sweep may remove is a prefix, bounded not only by each class's window
// but by the latest published anchor and by legal holds. That bound depends on
// all the classes' cutoffs together, which a per-class Source cannot see, so
// the sweeper hands a PrefixSource every class's cutoff at once.
type PrefixSource interface {
	// Store names the store in the report, such as "audit_logs".
	Store() string
	// Classes are the classes whose records live in the store.
	Classes() []pb.RetentionClass
	// CountPrefix counts, per class, the records older than that class's
	// cutoff (Older) and the part of them a purge would remove now (Eligible).
	// It reads and never removes.
	CountPrefix(ctx context.Context, cutoffs map[pb.RetentionClass]time.Time, heldOrgs []string) (PrefixCount, error)
	// RemovePrefix removes the prefix CountPrefix called eligible, at most
	// maxRows rows, and returns what it removed and the chain base after it.
	RemovePrefix(ctx context.Context, cutoffs map[pb.RetentionClass]time.Time, heldOrgs []string, maxRows int) (PrefixRemoval, error)
