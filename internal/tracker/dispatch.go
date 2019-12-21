package tracker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

// Dispatcher core (#2022): one pure tick that turns routed
// `scope:<role>` labels into runs, exactly once per issue. Everything it
// touches is injected — the forge (DispatchProvider), the run launcher
// (RunStarter), the clock, and the store — so the tick is testable with
// fakes and a real Postgres. See docs/architecture/issue-triggered-agents.md
// ("Dispatch state machine", "Exactly-once", "Data flow of the run input").

// The label names (LabelNeedsApproval, LabelAgent*) and the
// ReservedStateLabels set live in policy.go (#2024): an issue carrying
// any state label is skipped, and a human re-runs a finished issue by
// removing agent:done|failed. Only this code path writes them.

// DispatchProvider is the slice of the forge adapter a tick needs.
// Every WriterProvider satisfies it.
type DispatchProvider interface {
	ListIssues(ctx context.Context, c Conn, f IssueFilter) ([]Issue, error)
	GetIssue(ctx context.Context, c Conn, number int64) (Issue, error)
	SetLabels(ctx context.Context, c Conn, number int64, add, remove []string) error
	Comment(ctx context.Context, c Conn, number int64, body string) (Comment, error)
}

// StartRunRequest is what the dispatcher hands the run launcher.
// InputJSON is exactly the protojson of a TrackerDispatchInput — the
// issue reference, never its body.
type StartRunRequest struct {
	Username   string
	Connection string
	SkillID    string
	RunID      string
	InputJSON  string
	// RepoURL is the connection's repository (credential-free HTTPS
	// clone URL), so the run has a workspace to submit a doc change
	// from (#2023). Empty when the caller did not resolve one.
	RepoURL string
	// Lifecycle reports the run's start and end back to the dispatch
	// row (#2023). Never nil from Tick.
	Lifecycle RunLifecycle
}

// RunStarter starts a skill run bound to the connection. It returns once
// the run has started (or failed to); it does not wait for the run to
// finish. The production implementation goes through the same internal
// path as RunAgentSkill. At 10x this is the seam a durable pull queue
// slots in behind.
//
// A starter that accepts a run must call req.Lifecycle.RunStarted once
// the run is registered and before its agent is launched, and
// req.Lifecycle.RunEnded exactly once when the run ends. When
// RunStarted returns false the starter must end the run's lease, must
// not launch the agent, and returns ErrDispatchEnded.
type RunStarter interface {
	StartRun(ctx context.Context, req StartRunRequest) error
}

// DispatchStore is the persistence a tick needs. *Store satisfies it.
type DispatchStore interface {
	ListRoutes(ctx context.Context, username, connection string) ([]Route, error)
	InsertDispatch(ctx context.Context, d Dispatch) (*Dispatch, error)
	TransitionDispatch(ctx context.Context, id string, from, to pb.TrackerDispatchState, reason string, at time.Time) (bool, error)
	FailDispatch(ctx context.Context, id string, from pb.TrackerDispatchState, failure pb.TrackerDispatchFailure, reason string, at time.Time) (bool, error)
	ListDispatches(ctx context.Context, username, connection string, state pb.TrackerDispatchState) ([]Dispatch, error)
	SetDispatchLabelsPending(ctx context.Context, id string, pending bool) error
	// ListLabelsPendingDispatches and PriorDispatchLabelsPending find
	// terminal rows whose projection has not reached the forge (#2047,
	// #2052).
	ListLabelsPendingDispatches(ctx context.Context, username, connection string) ([]Dispatch, error)
	PriorDispatchLabelsPending(ctx context.Context, username, connection string, issue int64, excludeID string) (bool, error)
	DeleteQueuedDispatch(ctx context.Context, id string) (bool, error)
	RecordDispatchWarning(ctx context.Context, username, connection string, issue int64, scope string) (bool, error)
	ForgetDispatchWarning(ctx context.Context, username, connection string, issue int64, scope string) error
	// IssueDepth is the issue's recorded lineage depth (0 for a
	// human-created issue with no lineage row).
	IssueDepth(ctx context.Context, username, connection string, number int64) (int32, error)
}

var _ DispatchStore = (*Store)(nil)

// Dispatcher runs ticks for one connection's forge.
type Dispatcher struct {
	Store    DispatchStore
	Provider DispatchProvider
	// Conn is the forge connection (base URL, project) the provider
	// calls use. Its Credential is used as given only when Credentials
	// is nil; otherwise it is replaced at every call (see forgeConn).
	Conn Conn
	// Credentials resolves the connection's broker credential at each
	// forge call — every RunStarted / RunEnded / sweep / retry write —
	// never once per tick (#2269). RunEnded and the sweep can run up to
	// the policy's run timeout after the tick that started the run, by
	// when a rotated or short-lived credential (a GitHub App
	// installation token lives ~1h) resolved at tick time is stale. The
	// tenant is the dispatch's username; the secret is CredentialSecret.
	// nil uses Conn.Credential unchanged.
	Credentials CredentialSource
	// CredentialSecret names the connection's broker-only secret that
	// Credentials resolves.
	CredentialSecret string
	Runs             RunStarter
	Clock            Clock
	// RepoURL is the connection's repository clone URL, handed to every
	// run this dispatcher starts (StartRunRequest.RepoURL).
	RepoURL string
	// NewRunID mints the run id recorded on the row before the run
	// starts. nil uses a random UUID.
	NewRunID func() string
	// Policy is the connection's effective policy, read fresh every tick
	// (via effectivePolicy) so a max_depth lowered after a chain was filed
	// still holds (#2025), and its RunTimeout bounds how long a row may
	// stay active before the sweep fails it (#2026). nil means
	// PolicyFromProto(nil) — the documented defaults, never "unlimited",
	// so a caller that forgets to wire it fails closed.
	Policy *Policy
	// Leases is the daemon's view of its live dispatched runs: the sweep
	// ends a timed-out run's lease and fails rows with no live lease.
	// nil disables the lease-lost sweep and the lease end (the timeout
	// sweep still fails the row).
	Leases RunLeases
	// LeaseGrace is how long an active row may have no live lease before
	// it is failed LEASE_LOST. Zero means DefaultLeaseLostGrace.
	LeaseGrace time.Duration
	// Observer receives every terminal transition (#2026 metrics). nil
	// records nothing.
	Observer DispatchObserver
	// MaxStarts bounds how many runs one Tick starts (#2270). Only runs
	// that actually started count — not skips, not failed starts. An
	// eligible issue past the limit gets no row and no label (a later
	// tick picks it up) and is counted in TickResult.LeftUndispatched.
	// Zero or negative means unlimited; the RPC rejects negative.
	MaxStarts int32

	// leaseEndBudget overrides DefaultLeaseEndBudget (tests).
	leaseEndBudget time.Duration
}

// TickResult is one tick's outcome.
type TickResult struct {
	Started []Dispatch
	Failed  []Dispatch
	// TimedOut are active rows this tick's sweep failed (#2026): past the
	// run timeout (TIMEOUT) or with no live lease (LEASE_LOST).
	TimedOut []Dispatch
	// Counters are int32 to match DispatchTrackerIssuesResponse.
	SkippedNeedsApproval int32
	SkippedActive        int32
	SkippedUnrouted      int32
	// SkippedOverDepth counts issues whose recorded lineage depth
	// exceeds the policy's max_depth (#2025).
	SkippedOverDepth int32
	// LeftUndispatched counts issues that passed every check made before
	// the insert but were not dispatched because MaxStarts runs had
	// already started (#2270).
	LeftUndispatched int32
}

// Tick lists the connection's open issues and, for each one with a
// routed scope label, no state label, no approval gate and a lineage
// depth within the policy's max_depth: inserts a
// dispatch row (the exactly-once index decides the winner), starts the
// routed skill with the issue reference as input, and labels the issue
// agent:queued. A start error fails the row, labels agent:failed and
// comments the reason. An unrouted scope label gets one stamped warning
// comment, ever.
//
// The row, not the label, says whether an issue was already handled
// (#2047, #2052): an issue whose latest row is terminal with
// labels_pending — its agent:done|failed never reached the forge — is
// skipped as if the label were there, and the tick retries that
// projection first, until it lands or a human puts the state label on.
//
// A forge list error or a store error aborts the tick; per-issue forge
// write failures do not (they are recorded and the tick moves on).
func (d *Dispatcher) Tick(ctx context.Context, username, connection string) (TickResult, error) {
	var res TickResult
	policy := d.effectivePolicy()
	routes, err := d.Store.ListRoutes(ctx, username, connection)
	if err != nil {
		return res, fmt.Errorf("list routes: %w", err)
	}
	skillFor := make(map[string]string, len(routes))
	for _, r := range routes {
		skillFor[r.Scope] = r.SkillID
	}

	// Stuck rows first (#2026): a run past its timeout, or a row whose
	// run this daemon no longer holds, is failed and projected before
	// anything new starts.
	if res.TimedOut, err = d.sweep(ctx, username, connection); err != nil {
		return res, err
	}
	// Then terminal rows whose labels never landed: retried, and their
	// issues held back from a second dispatch while they still lag.
	held, err := d.retryPendingLabels(ctx, username, connection)
	if err != nil {
		return res, err
	}

	conn, err := d.forgeConn(ctx, username)
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}
	issues, err := d.Provider.ListIssues(ctx, conn, IssueFilter{State: pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN})
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}

	for _, issue := range issues {
		// A cancelled tick stops here rather than failing every remaining
		// issue's start for a reason that has nothing to do with it.
		if err := ctx.Err(); err != nil {
			return res, err
		}
		scopes := scopeLabels(issue.Labels)
		if len(scopes) == 0 {
			continue // not addressed to any role
		}
		if hasAnyLabel(issue.Labels, LabelNeedsApproval) {
			res.SkippedNeedsApproval++
			continue
		}
		if hasAnyLabel(issue.Labels, ReservedStateLabels...) || held[issue.Number] {
			res.SkippedActive++
			continue
		}

		scope, skillID := "", ""
		for _, sc := range scopes {
			if sk, ok := skillFor[sc]; ok {
				if scope == "" {
					scope, skillID = sc, sk
				}
				continue
			}
			if err := d.warnUnrouted(ctx, username, connection, issue.Number, sc); err != nil {
				return res, err
			}
		}
		if scope == "" {
			res.SkippedUnrouted++
			continue
		}

		// Chain depth (#2025), enforced by the daemon from the lineage
		// table — never from anything on the issue. Create already refuses
		// a child past max_depth; this holds a policy lowered afterwards.
		depth, err := d.Store.IssueDepth(ctx, username, connection, issue.Number)
		if err != nil {
			return res, fmt.Errorf("read depth of #%d: %w", issue.Number, err)
		}
		if !policy.DepthAllowed(depth) {
			res.SkippedOverDepth++
			continue
		}

		// The caller's budget (#2270): once MaxStarts runs have started,
		// an issue that got this far is left for a later tick — no row, no
		// label. Checked before the insert so nothing is written for it.
		if d.MaxStarts > 0 && len(res.Started) >= int(d.MaxStarts) {
			res.LeftUndispatched++
			continue
		}

		// From the insert until StartRun has succeeded, no cancellation
		// point may leave the row QUEUED with no run behind it (#2049):
		// the active-row index would skip the issue on every later tick,
		// and nothing sweeps queued rows. So the writes in this window run
		// on a detached, bounded context, and a tick cancelled inside it
		// removes the row (nothing is on the forge yet) or fails it
		// (agent:queued is, or may be, on the issue) — never leaves it.
		//
		// The insert itself is detached: a cancel landing while it is in
		// flight could commit the row yet report an error.
		ictx, icancel := bookkeepingContext(ctx)
		row, err := d.Store.InsertDispatch(ictx, Dispatch{
			Username: username, Connection: connection, IssueNumber: issue.Number,
			Scope: scope, SkillID: skillID, RunID: d.newRunID(), CreatedAt: d.Clock.Now(), Depth: depth,
		})
		icancel()
		if errors.Is(err, ErrDispatchActive) {
			res.SkippedActive++ // another dispatcher (or a live run) holds it
			continue
		}
		if err != nil {
			return res, fmt.Errorf("insert dispatch for #%d: %w", issue.Number, err)
		}
		if cerr := ctx.Err(); cerr != nil {
			if err := d.abandon(ctx, row); err != nil {
				return res, err
			}
			return res, cerr
		}

		// The previous generation may have gone terminal after the retry
		// above and before this insert, with its projection failing (or
		// still in flight): the issue shows no state label, but it was
		// handled. The row says so; the label cannot (#2047, #2052). A
		// failed read (a cancelled tick included) abandons the row too.
		if prior, err := d.Store.PriorDispatchLabelsPending(ctx, username, connection, issue.Number, row.ID); err != nil || prior {
			if aerr := d.abandon(ctx, row); aerr != nil {
				return res, errors.Join(err, aerr)
			}
			if err != nil {
				return res, fmt.Errorf("read prior dispatch of #%d: %w", issue.Number, err)
			}
			res.SkippedActive++
			continue
		}

		// Re-read before starting anything (#2023): the list above can be
		// stale. A peer may have dispatched this issue AND finished it
		// between our list and our insert — its row is terminal, so the
		// insert succeeded, but the issue now carries agent:done and no
		// scope label. Starting here would be a second run.
		if keep, err := d.stillDispatchable(ctx, row, scope); err != nil {
			return res, err
		} else if !keep {
			res.SkippedActive++
			continue
		}

		// The row is authoritative; the label is its projection. It goes
		// on before the start (provisioning can take minutes) and a forge
		// failure leaves labels_pending for the retry (#2026). Detached, so
		// a cancel mid-write cannot leave the row with neither the label
		// nor labels_pending; the tick's cancellation is checked after.
		if err := d.labelQueued(ctx, row); err != nil {
			return res, d.failBeforeStart(ctx, row, err)
		}
		if cerr := ctx.Err(); cerr != nil {
			// A cancelled tick starts no new run.
			failed, err := d.failStart(ctx, row, cerr)
			if err != nil {
				return res, err
			}
			res.Failed = append(res.Failed, *failed)
			return res, cerr
		}

		input, err := dispatchInputJSON(row)
		if err != nil {
			return res, d.failBeforeStart(ctx, row, err)
		}
		lc := &dispatchRun{d: d, row: *row}
		if startErr := d.Runs.StartRun(ctx, StartRunRequest{
			Username: username, Connection: connection, SkillID: skillID, RunID: row.RunID, InputJSON: input,
			RepoURL: d.RepoURL, Lifecycle: lc,
		}); startErr != nil {
			if errors.Is(startErr, ErrDispatchEnded) {
				// Swept while it was still provisioning: the sweep already
				// failed and projected the row, and the starter tore the
				// run down. Nothing to report a second time.
				continue
			}
			failed, err := d.failStart(ctx, row, startErr)
			if err != nil {
				return res, err
			}
			res.Failed = append(res.Failed, *failed)
			continue
		}
		if lc.started.Load() {
			row.State = pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_RUNNING
		}
		res.Started = append(res.Started, *row)
	}
	return res, nil
}

