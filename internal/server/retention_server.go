package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/retention"
	retentionpg "github.com/footprintai/containarium/internal/retention/pgstore"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Audit actions written by RetentionService changes (#2875).
const (
	retentionPolicySetAction   = "retention.policy.set"
	retentionHoldSetAction     = "retention.hold.set"
	retentionHoldReleaseAction = "retention.hold.release"
	// retentionModeSetAction and retentionSweepAction are the audit actions of
	// the mode switch and of an enforced sweep (#2877).
	retentionModeSetAction = "retention.mode.set"
	retentionSweepAction   = "retention.sweep"
	// retentionSweepResultAction is the second entry of a purge of the
	// hash-chained audit_logs (#2979): the retention.sweep entry is written
	// before the purge and can name only the chain base it started from, so
	// this one, written after, names the base it ended on.
	retentionSweepResultAction = "retention.sweep.result"
)

// retentionSweeperActor is the username on the audit entry of an enforced
// sweep, which no admin runs.
const retentionSweeperActor = "retention-sweeper"

// retentionModeAuditDetail is the Detail of a retention.mode.set entry.
type retentionModeAuditDetail struct {
	PreviousMode string `json:"previous_mode"`
	Mode         string `json:"mode"`
}

// retentionSweepAuditStore is one store in a retention.sweep entry: counts
// only, never the content of a record.
type retentionSweepAuditStore struct {
	Class      string `json:"class"`
	Store      string `json:"store"`
	WindowDays int32  `json:"window_days"`
	Eligible   int64  `json:"eligible"`
	Held       int64  `json:"held"`
	// PreviousBaseID is the chain base the sweep starts from, for the
	// audit_logs store; absent when the store has no chain base (#2979).
	PreviousBaseID int64 `json:"previous_base_id,omitempty"`
}

// retentionSweepAuditDetail is the Detail of a retention.sweep entry, written
// before an enforced sweep removes anything. Eligible is what the sweep will
// try to remove; a store with a backlog above retention.MaxRemovePerStore is
// worked off over the following sweeps.
type retentionSweepAuditDetail struct {
	PolicyRevision int64                      `json:"policy_revision"`
	ActiveHolds    int64                      `json:"active_holds"`
	Stores         []retentionSweepAuditStore `json:"stores"`
}

// retentionSweepResultClass is the rows one class lost in a purge.
type retentionSweepResultClass struct {
	Class   string `json:"class"`
	Removed int64  `json:"removed"`
}

// retentionSweepResultAuditDetail is the Detail of a retention.sweep.result
// entry: ids and counts only, never the content of a row (#2979).
type retentionSweepResultAuditDetail struct {
	Store          string                      `json:"store"`
	PreviousBaseID int64                       `json:"previous_base_id,omitempty"`
	BaseID         int64                       `json:"base_id"`
	RowsRemoved    int64                       `json:"rows_removed"`
	Classes        []retentionSweepResultClass `json:"classes"`
}

// retentionAuditWindow is one changed class in a retention.policy.set entry.
type retentionAuditWindow struct {
	Class string `json:"class"`
	Days  int32  `json:"days"`
}

// retentionPolicyAuditDetail is the Detail of a retention.policy.set entry:
// the revisions and the classes the call changed.
type retentionPolicyAuditDetail struct {
	PreviousRevision int64                  `json:"previous_revision"`
	Revision         int64                  `json:"revision"`
	Windows          []retentionAuditWindow `json:"windows"`
}

// retentionHoldAuditDetail is the Detail of a hold set or release entry.
type retentionHoldAuditDetail struct {
	OrgID  string `json:"org_id"`
	Reason string `json:"reason"`
	SetBy  string `json:"set_by"`
}

// errRetentionAuditFailed marks a beforeCommit audit write that failed; the
// change was rolled back.
var errRetentionAuditFailed = errors.New("audit write failed")

// errRetentionEnforceNeedsAudit is why ENFORCE is refused on a daemon with no
// audit store.
var errRetentionEnforceNeedsAudit = errors.New("enforce mode needs an audit store: this daemon has none (it needs Postgres, REST enabled and a started audit store), so deletions could not be recorded; the mode stays as it is")

// errRetentionStoreUnavailable is what every call returns while Postgres is
// configured but the retention store could not be installed at startup.
var errRetentionStoreUnavailable = errors.New("retention store unavailable: Postgres is configured but the retention store was not installed (restart the daemon once Postgres answers)")

// unavailableRetentionStore fails closed: an in-memory stand-in would accept
// holds that a restart silently drops.
type unavailableRetentionStore struct{}

func (unavailableRetentionStore) GetPolicy(context.Context) (*pb.RetentionPolicy, error) {
	return nil, errRetentionStoreUnavailable
}
func (unavailableRetentionStore) SetPolicy(context.Context, []*pb.RetentionWindow, string, func(*retention.PolicySetResult) error) (*retention.PolicySetResult, error) {
	return nil, errRetentionStoreUnavailable
}
func (unavailableRetentionStore) GetMode(context.Context) (*pb.RetentionModeState, error) {
	return nil, errRetentionStoreUnavailable
}
func (unavailableRetentionStore) SetMode(context.Context, pb.RetentionMode, string, func(*retention.ModeSetResult) error) (*retention.ModeSetResult, error) {
	return nil, errRetentionStoreUnavailable
}
func (unavailableRetentionStore) ListHolds(context.Context) ([]*pb.LegalHold, error) {
	return nil, errRetentionStoreUnavailable
}
func (unavailableRetentionStore) SetHold(context.Context, string, string, string, func(*pb.LegalHold) error) (*pb.LegalHold, error) {
	return nil, errRetentionStoreUnavailable
}
func (unavailableRetentionStore) ReleaseHold(context.Context, string, string, func(*pb.LegalHold) error) (*pb.LegalHold, error) {
	return nil, errRetentionStoreUnavailable
}

// retentionStartupStore picks the store: in-memory with no Postgres (logged,
// since nothing survives a restart), the Postgres store when it was
// installed, otherwise the fail-closed stand-in.
func retentionStartupStore(postgresConfigured bool, pg retention.Store) retention.Store {
	switch {
	case !postgresConfigured:
		log.Printf("Retention: no database configured; using the in-memory store (a policy or legal hold set now is lost on restart)")
		return retention.NewMemoryStore()
	case pg != nil:
		return pg
	default:
		log.Printf("Warning: retention store UNAVAILABLE: Postgres is configured but its store was not installed; retention calls fail until the daemon restarts with Postgres reachable")
		return unavailableRetentionStore{}
	}
}

// retentionSweepInterval is how often the daemon recounts what a sweep would
// remove. The report carries its generated_at, and refresh=true sweeps now.
const retentionSweepInterval = time.Hour

// newRetentionReporter builds the reporter over the retention store
// and, when the daemon has a database pool, the Postgres sources.
func newRetentionReporter(store retention.Store, pool *pgxpool.Pool) *retention.Reporter {
	unswept := retention.ChainAndBundles()
	var sources []retention.Source
	if pool != nil {
		sources = retentionpg.NewSources(pool)
	} else {
		unswept = append(unswept, retentionpg.NoDatabase()...)
	}
	return retention.NewReporter(retention.NewSweeper(store, sources, unswept))
}

// retentionAnchorFunc is the anchor that bounds a purge: the lower of the local
// file's and the external bucket's latest checkpoint (audit.SafeAnchor). It
// fails closed. With a bucket configured but not built (a bad URI, no gcloud) it
// returns ok=false, so the purge gets no anchor at all rather than a file-only
// one: turning the external copy on can never be a way to purge against less
// evidence than the operator asked for.
func retentionAnchorFunc(file *audit.FileSink, bucket *audit.GCSAnchorSink, bucketURI string) (retentionpg.AnchorFunc, bool) {
	var external audit.RootSink // stays a nil interface without a bucket
	switch {
	case bucket != nil:
		external = bucket
	case strings.TrimSpace(bucketURI) != "":
		return nil, false
	}
	return func(ctx context.Context) (retentionpg.Anchor, bool, error) {
		a, ok, err := audit.SafeAnchor(ctx, file, external)
		return retentionpg.Anchor{Checkpoint: a.Checkpoint, Root: a.Root}, ok, err
	}, true
}

// RetentionServer serves the retention policy and legal holds (#2875). Every
// RPC is admin-only; changes are audited, and a failed audit write fails the
// change. The server itself deletes nothing: GetRetentionReport only counts,
// and the periodic sweep removes records only while the mode is ENFORCE.
type RetentionServer struct {
	pb.UnimplementedRetentionServiceServer
	store    retention.Store
	audit    auditLogger
	reporter *retention.Reporter
}

func NewRetentionServer(store retention.Store) *RetentionServer {
	return &RetentionServer{store: store}
}

// SetStore sets the backing store at startup, before the server serves.
func (s *RetentionServer) SetStore(store retention.Store) { s.store = store }

// SetReporter wires the sweeper's reporter (#2876) at startup. With none,
// GetRetentionReport is UNAVAILABLE.
func (s *RetentionServer) SetReporter(r *retention.Reporter) {
	s.reporter = r
	s.wireSweepAudit()
}

// wireSweepAudit gives the reporter's sweeper the audit entry an enforced
// sweep writes, once both the reporter and the audit store are known.
func (s *RetentionServer) wireSweepAudit() {
	if s.reporter != nil && s.audit != nil {
		s.reporter.SetAuditor(s.auditSweep)
		s.reporter.SetResultAuditor(s.auditSweepResult)
	}
}

// auditSweep writes the retention.sweep entry of an enforced sweep.
func (s *RetentionServer) auditSweep(ctx context.Context, rep *pb.RetentionReport) error {
	d := retentionSweepAuditDetail{PolicyRevision: rep.GetPolicyRevision(), ActiveHolds: rep.GetActiveHolds(), Stores: []retentionSweepAuditStore{}}
	for _, st := range rep.GetStores() {
		if st.GetStatus() != pb.RetentionSweepStatus_RETENTION_SWEEP_STATUS_COUNTED {
			continue
		}
		d.Stores = append(d.Stores, retentionSweepAuditStore{
			Class: st.GetClass().String(), Store: st.GetStore(), WindowDays: st.GetWindowDays(),
			Eligible: st.GetEligible(), Held: st.GetHeld(),
			PreviousBaseID: st.GetChainBase().GetBaseId(),
		})
	}
	return s.writeAudit(ctx, retentionSweeperActor, retentionSweepAction, "retention_sweep", "cluster", d)
}

// auditSweepResult writes the retention.sweep.result entry of a prefix purge
// that removed rows (#2979): the chain base the purge ended on, written after
// the rows are gone because it did not exist before. The sweeper treats a
// failure here as non-fatal, since the purge is already committed.
func (s *RetentionServer) auditSweepResult(ctx context.Context, r retention.PurgeResult) error {
	d := retentionSweepResultAuditDetail{
		Store: r.Store, PreviousBaseID: r.PreviousBaseID, BaseID: r.BaseID, RowsRemoved: r.RowsRemoved,
		Classes: []retentionSweepResultClass{},
	}
	for class, n := range r.ByClass {
		if n > 0 {
			d.Classes = append(d.Classes, retentionSweepResultClass{Class: class.String(), Removed: n})
		}
	}
	sort.Slice(d.Classes, func(i, j int) bool { return d.Classes[i].Class < d.Classes[j].Class })
	return s.writeAudit(ctx, retentionSweeperActor, retentionSweepResultAction, "retention_sweep", "cluster", d)
}

// SetAuditStore wires the audit store. With none, changes succeed and write
// no entry (no Postgres, REST disabled, or an audit store that failed to
// start), the same as the guardrail baseline switch.
func (s *RetentionServer) SetAuditStore(a auditLogger) {
	if a != nil {
		s.audit = a
		s.wireSweepAudit()
		s.auditUpgradeChanges()
	}
}

