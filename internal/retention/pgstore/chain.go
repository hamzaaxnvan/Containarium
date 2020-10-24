package pgstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/retention"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Anchor is the newest published anchor a purge must stay below.
type Anchor struct {
	Checkpoint int64
	Root       string
}

// AnchorFunc reads the anchor that bounds a purge. ok is false when nothing is
// anchored yet. audit.SafeAnchor is the daemon's implementation.
type AnchorFunc func(ctx context.Context) (a Anchor, ok bool, err error)

// ChainSource is the retention.PrefixSource of the hash-chained audit_logs
// (#2878, slice D of #2576; also #2420).
//
// audit_logs is one chain for every org and every class, so a purge removes a
// PREFIX only: the ids below one end id. The end is the lowest of
//
//   - the latest published anchor's checkpoint: the anchored row and everything
//     after it stay, so verify-anchor still has the row it compares;
//   - the first row not yet past its own class window (session actions use the
//     session window, every other action the gateway-logs window): rows after
//     an unexpired row cannot go, however old;
//   - the oldest row a legal hold keeps: a row of a held org, or, while any
//     hold is active, a row with no org (Q21 on #2727).
//
// Other audit actions inside the prefix are removed with it, since a prefix
// cannot skip them.
//
// The base (the last purged row's id and hash) is written in the same
// transaction as each batch of deletes, so a verifier never sees rows gone
// without the base that explains them.
type ChainSource struct {
	pool *pgxpool.Pool

	mu     sync.RWMutex
	anchor AnchorFunc
}

// NewChainSource builds the source over the daemon's database pool. Until
// SetAnchor is called it can purge nothing.
func NewChainSource(pool *pgxpool.Pool) *ChainSource { return &ChainSource{pool: pool} }

// SetAnchor wires the anchor that bounds a purge. It may be called while the
// sweeper is running.
func (c *ChainSource) SetAnchor(f AnchorFunc) {
	c.mu.Lock()
	c.anchor = f
	c.mu.Unlock()
}

func (c *ChainSource) Store() string { return "audit_logs" }

func (c *ChainSource) Classes() []pb.RetentionClass {
	return []pb.RetentionClass{
		pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS,
		pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS,
	}
}

// removeChainBatch is how many rows one transaction removes.
const removeChainBatch = 1000

// prefixPlan is where a purge ends and why.
type prefixPlan struct {
	// end: rows with an id below it may go. 0 means nothing.
	end int64
	// skipped: why nothing can be purged at all, such as no anchor.
	skipped string
	// note: what bounded the purge, for the report.
	note string
}

// cutoffsOf returns the gateway-logs and session-transcripts cutoffs.
func cutoffsOf(cutoffs map[pb.RetentionClass]time.Time) (gw, sess time.Time, err error) {
	gw, okG := cutoffs[pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS]
	sess, okS := cutoffs[pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS]
	if !okG || !okS {
		return gw, sess, errors.New("both chained classes need a cutoff")
	}
	return gw, sess, nil
}

// plan decides where the purge ends. q reads inside the caller's transaction
// when there is one, so the holds it sees are the ones the caller has locked.
func (c *ChainSource) plan(ctx context.Context, q audit.RowQuerier, cutoffs map[pb.RetentionClass]time.Time, heldOrgs []string) (prefixPlan, error) {
	gwCut, sessCut, err := cutoffsOf(cutoffs)
	if err != nil {
		return prefixPlan{}, err
	}

	c.mu.RLock()
	anchorFn := c.anchor
	c.mu.RUnlock()
	if anchorFn == nil {
		return prefixPlan{skipped: "no anchor source is wired on this daemon, so nothing can be purged"}, nil
	}
	a, ok, err := anchorFn(ctx)
	if err != nil {
		return prefixPlan{skipped: fmt.Sprintf("the anchor could not be read (%v), so nothing is purged", err)}, nil
	}
	if !ok {
		return prefixPlan{skipped: "no anchor has been published (or the external copy has none yet), so nothing can be purged"}, nil
	}

	var base int64
	if b, purged, err := audit.ReadChainBase(ctx, q); err != nil {
		return prefixPlan{}, err
	} else if purged {
		base = b.ID
	}
	if a.Checkpoint <= base+1 {
		return prefixPlan{note: fmt.Sprintf("nothing below the latest anchor (id %d) is left to purge", a.Checkpoint)}, nil
	}

	// The anchored row must still match its anchor. If it does not, a rewrite
	// is already suspected and verify-anchor needs the rows a purge would
	// delete as context: refuse.
	var rowHash string
	switch err := q.QueryRow(ctx, `SELECT row_hash FROM audit_logs WHERE id = $1`, a.Checkpoint).Scan(&rowHash); {
	case errors.Is(err, pgx.ErrNoRows):
		return prefixPlan{skipped: fmt.Sprintf("the anchored row (id %d) is not in the database; run 'containarium audit verify-anchor'. Nothing is purged", a.Checkpoint)}, nil
	case err != nil:
		return prefixPlan{}, err
	case rowHash != a.Root:
		return prefixPlan{skipped: fmt.Sprintf("the anchored row (id %d) no longer matches its anchor; run 'containarium audit verify-anchor'. Nothing is purged", a.Checkpoint)}, nil
	}

	end, bound := a.Checkpoint, fmt.Sprintf("the latest anchor (id %d)", a.Checkpoint)

	// First row not yet past its own class window.
	var firstKept *int64
	if err := q.QueryRow(ctx, `
		SELECT min(id) FROM audit_logs
		WHERE (action = ANY($1) AND timestamp >= $2)
		   OR (NOT (action = ANY($1)) AND timestamp >= $3)`,
		audit.SessionTranscriptActions, sessCut, gwCut).Scan(&firstKept); err != nil {
		return prefixPlan{}, err
	}
	if firstKept != nil && *firstKept < end {
		end, bound = *firstKept, fmt.Sprintf("the first row inside its window (id %d)", *firstKept)
	}

	// Oldest row a hold keeps.
	if len(heldOrgs) > 0 {
		var held *int64
		if err := q.QueryRow(ctx, `SELECT min(id) FROM audit_logs WHERE org_id = '' OR org_id = ANY($1)`, heldOrgs).Scan(&held); err != nil {
			return prefixPlan{}, err
		}
		if held != nil && *held < end {
			end, bound = *held, fmt.Sprintf("the oldest row a legal hold keeps (id %d; with a hold active, rows with no org count as held)", *held)
		}
	}
	return prefixPlan{end: end, note: "the purge stops below " + bound + "; other audit actions in the prefix are removed with it"}, nil
}

// CountPrefix counts without removing anything.
func (c *ChainSource) CountPrefix(ctx context.Context, cutoffs map[pb.RetentionClass]time.Time, heldOrgs []string) (retention.PrefixCount, error) {
	gwCut, sessCut, err := cutoffsOf(cutoffs)
	if err != nil {
		return retention.PrefixCount{}, err
	}
	p, err := c.plan(ctx, c.pool, cutoffs, heldOrgs)
	if err != nil {
		return retention.PrefixCount{}, wrapMissingTable("count audit_logs", err)
