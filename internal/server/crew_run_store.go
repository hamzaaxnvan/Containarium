package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// CrewRunStore records crew executions, keyed by run id. Two impls, selected
// the same way NetworkPolicyStore is: PostgresCrewRunStore when the daemon has
// a DB pool, MemCrewRunStore for --standalone daemons and tests.
//
// Durability is the point (#1182). A daemon restart used to lose every
// in-flight run: the run simply never completed, and nothing recorded that it
// had existed. GetCrewRun returned NotFound for work that really did happen.
//
// Entries are cloned on the way in and on the way out. Handing out the same
// pointer the caller keeps mutating would let a reader observe a run
// mid-write — a proto message is several fields and nothing makes updating
// them atomic. That property predates this interface and is preserved by both
// impls; the Postgres one gets it for free by serializing.
type CrewRunStore interface {
	// Put records a run, overwriting any previous state for the same id.
	Put(ctx context.Context, r *pb.CrewRun) error
	// Get returns the run and whether it was found.
	Get(ctx context.Context, id string) (*pb.CrewRun, bool, error)
	// FailStranded marks runs still RUNNING as FAILED with the given reason,
	// returning how many it changed.
	//
	// Scoped to `owner` — this daemon's backend id — plus runs with no
	// recorded owner. A run owned by a PEER is never touched: several daemons
	// can share one database, and one restarting must not fail another's
	// in-flight runs (#1322). Unowned runs predate ownership and are still
	// swept, because on the single-daemon deployment that is the default they
	// are always this daemon's own, and leaving them would strand every
	// pre-upgrade run forever.
	//
	// Called once at daemon start. RunCrew records a run as RUNNING before it
	// drives it, so a daemon that dies mid-run leaves that state behind — and
	// with a durable store it survives, so GetCrewRun answers RUNNING forever
	// for a run that can never finish (#1182 AC4). Nothing else reconciles
	// them: driveCrew has no resumption point, so the honest outcome is a
	// terminal failure that says why, not a run that claims to be working.
	FailStranded(ctx context.Context, owner, reason string) (int, error)
	// DeleteSkillRun removes the record for a standalone skill run — crew_id
	// empty, skill_id set, put by CrewServer.recordSkillRun for a RunAgentSkill
	// run (#2122) so TailRunLog can resolve it durably after a restart.
	// Coupled to the run-journal reaper's retention window rather than kept
	// forever, unlike a real crew run's record.
	//
	// A no-op for any id that names an actual crew run (crew_id set) or that
	// isn't present at all: the run-journal reaper calls this for every run id
	// whose journal directory it reaps, crew and skill alike, and must never
	// be able to delete a crew's record. The guard lives here, in the store,
	// rather than at the reaper's call site, so it holds regardless of caller.
	DeleteSkillRun(ctx context.Context, id string) error
}

// StrandedByRestart is the reason recorded for a run the daemon was driving
// when it stopped. Named so an operator reading a failed run can tell this
// apart from a crew that genuinely failed.
const StrandedByRestart = "daemon restarted while this run was in flight; the run was not resumed"

// --- in-memory ------------------------------------------------------

// MemCrewRunStore is a goroutine-safe in-memory store. Runs do not survive a
// daemon restart — used on --standalone daemons (no Postgres) and in tests.
type MemCrewRunStore struct {
	mu   sync.RWMutex
	runs map[string]*pb.CrewRun
}

func NewMemCrewRunStore() *MemCrewRunStore {
	return &MemCrewRunStore{runs: make(map[string]*pb.CrewRun)}
}

func (s *MemCrewRunStore) Put(_ context.Context, r *pb.CrewRun) error {
	if r == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.GetId()] = proto.Clone(r).(*pb.CrewRun)
	return nil
}

func (s *MemCrewRunStore) Get(_ context.Context, id string) (*pb.CrewRun, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return nil, false, nil
	}
	return proto.Clone(r).(*pb.CrewRun), true, nil
}

func (s *MemCrewRunStore) FailStranded(_ context.Context, owner, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, r := range s.runs {
		if r.GetState() != pb.CrewRunState_CREW_RUN_STATE_RUNNING {
			continue
		}
		if !ownedForReconciliation(r.GetOwner(), owner) {
			continue
		}
		updated := proto.Clone(r).(*pb.CrewRun)
		updated.State = pb.CrewRunState_CREW_RUN_STATE_FAILED
		updated.Error = reason
		s.runs[id] = updated
		n++
	}
	return n, nil
}

func (s *MemCrewRunStore) DeleteSkillRun(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok && r.GetCrewId() == "" {
		delete(s.runs, id)
	}
	return nil
}

// --- postgres -------------------------------------------------------

// PostgresCrewRunStore persists runs so they survive a daemon restart.
type PostgresCrewRunStore struct {
	pool *pgxpool.Pool
}

// NewPostgresCrewRunStore creates the table if it does not exist.
//
// The run body is stored as protojson rather than a column per field. The
// CrewRun message is a small bag of strings today, but the columns that
// matter for querying — id and state — are lifted out and indexed, so adding
// a proto field later needs no migration. That is the shape google/ax uses
