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
