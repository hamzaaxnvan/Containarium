package retention

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Count is what one store holds past a cutoff.
type Count struct {
	// Older is every record older than the cutoff, held or not.
	Older int64
	// Eligible is the part of Older that no legal hold keeps.
	Eligible int64
}

// Unattributed is the Count of a store whose records carry no org. While any
// hold is active such a record counts as held (Q21 on #2727): the sweeper
// cannot tell whose it is, so it keeps it.
func Unattributed(older int64, heldOrgs []string) Count {
	if len(heldOrgs) > 0 {
		return Count{Older: older}
	}
	return Count{Older: older, Eligible: older}
}

// Source counts one store's records of one class. It has no delete: a count
// must never be able to remove anything, so enforcement (#2877) is the
// separate Remover interface that a source may also implement.
type Source interface {
	Class() pb.RetentionClass
	// Store names the store in the report, such as a table name.
	Store() string
	// Count counts the records older than cutoff. heldOrgs are the orgs with
	// an active legal hold, ordered by org id; a store with no org column
	// returns Unattributed.
	Count(ctx context.Context, cutoff time.Time, heldOrgs []string) (Count, error)
}

// MaxRemovePerStore bounds how many records one sweep removes from one store.
// A backlog larger than this is worked off over the following sweeps, so a
// first enforced sweep over years of data cannot hold the database for long.
const MaxRemovePerStore = 100_000

// Remover deletes the records a Source counts as eligible. The cutoff and the
// held orgs are the ones Count takes, and Remove must apply the same
// predicate, so a sweep deletes exactly what its report listed: records older
// than the cutoff that no legal hold keeps. It removes at most maxRows and
// returns how many it removed.
type Remover interface {
	Remove(ctx context.Context, cutoff time.Time, heldOrgs []string, maxRows int) (Removal, error)
}

// Removal is what one Remove did.
type Removal struct {
	Removed int64
	// Skipped says why a store could not be swept at all (a table that does not
	// exist, so no hold could be ruled out), so a silent zero is not mistaken
	// for "nothing to remove".
	Skipped string
}

// Auditor records an enforced sweep before anything is removed. The report it
// receives carries the per-class, per-store counts and never record content.
// Its error stops the sweep: nothing is removed without its audit entry.
type Auditor func(ctx context.Context, rep *pb.RetentionReport) error

// PurgeResult is what a prefix purge of a chained store did, for the second
// audit entry (#2979). The Auditor entry is written before anything is removed,
// so it can name only the base the purge started from; the base the purge
// ended on exists only afterwards, and this is how it reaches the log. Ids and
// counts only, never record content.
type PurgeResult struct {
	Store string
	// PreviousBaseID is the chain base before the purge, 0 when there was none.
	PreviousBaseID int64
	// BaseID is the chain base after the purge.
	BaseID int64
	// RowsRemoved is the number of rows this purge removed.
	RowsRemoved int64
	// ByClass is RowsRemoved per class.
	ByClass map[pb.RetentionClass]int64
}

// ResultAuditor records a PurgeResult after the rows are gone. The purge is
// already committed when it runs, so its error cannot undo it: it is noted on
// the report entry and logged, and the sweep still succeeds.
type ResultAuditor func(ctx context.Context, r PurgeResult) error

// Unswept is a store the sweeper lists but does not count, with why.
type Unswept struct {
	Class pb.RetentionClass
	Store string
	Note  string
}

// ChainAndBundles lists the classes the sweeper does not count by default: the
// hash-chained audit_logs classes (slice D, #2878) and evidence bundles
// (slice E, #2879; no bundle store exists yet). The chained classes drop off
// the list when a PrefixSource for audit_logs is added, which the daemon does
// once it has an audit store.
func ChainAndBundles() []Unswept {
	const chained = "in the hash-chained audit_logs: only the oldest rows below the latest anchor can be purged, with a stored chain base (#2878). This daemon has no audit store (it needs Postgres with REST enabled), so it is not swept"
	return []Unswept{
