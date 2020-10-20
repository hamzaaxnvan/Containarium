package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The run journal's read path (#2096). The in-box runtime appends one
// JSON-lines file per run per member box (#2095, agent-runtime/src/journal.ts);
// TailRunLog serves byte windows of it, resumable by offset.
const (
	// runJournalRoot mirrors journal.ts JOURNAL_ROOT.
	runJournalRoot = "/var/log/agent-runtime/runs"

	tailRunLogDefaultBytes = 65536
	tailRunLogMaxBytes     = 262144
	tailRunLogMaxFollow    = 10
	tailRunLogPoll         = 500 * time.Millisecond

	// runEndedMarker is the prefix of the status line runJournaled writes
	// last ("run ended exit=N"), as JSON.stringify lays out its fields.
	runEndedMarker = `"kind":"status","text":"run ended`
)

// gatewayCallReader is the model gateway's per-run call log (modelgateway.CallLog).
type gatewayCallReader interface {
	Since(runID, skillID string, after int64) ([]*pb.GatewayCall, int64)
}

// SetGatewayCalls lets TailRunLog return the model-gateway calls recorded for a
// run next to its journal (#2928). Nil (the default) returns none.
func (s *AgentSkillServer) SetGatewayCalls(r gatewayCallReader) { s.gatewayCalls = r }

// boxScriptFunc runs a POSIX shell script inside a box and returns its stdout.
// A non-zero exit is an error. Production wraps the container manager; tests
// fake it.
type boxScriptFunc func(box, script string) (string, error)

// crewRunMembersFunc resolves a crew run id to its members' skill ids, entry
// skill first. ok=false means no crew run has that id.
type crewRunMembersFunc func(ctx context.Context, runID string) (members []string, ok bool, err error)

// recordSkillRunFunc persists a durable run->skill record for a standalone
// skill run (#2122), via the crew-run record store crew runs already use —
// CrewServer.recordSkillRun in production, wired by NewCrewServer the same
// way crewRunMembers is. Nil is a valid, silent no-op: a daemon (or test)
// with no CrewServer wired just keeps pre-#2122 behavior — the in-memory
// runIndex resolves the run until this process exits, and a restart loses it,
// exactly as before.
type recordSkillRunFunc func(ctx context.Context, runID, skillID string) error

// reapSkillRunFunc removes a standalone skill run's durable record, called by
// the run-journal reaper for every run id whose journal directory it reaps
// (#2122) — crew and skill run ids alike. Implementations must ignore any id
// that names a crew run; CrewServer.reapSkillRunRecord does, via
// CrewRunStore.DeleteSkillRun. Nil is a valid, silent no-op.
type reapSkillRunFunc func(ctx context.Context, runID string) error

// memberBox is the container a skill's box runs in (provisionSkillBox).
func memberBox(skillID string) string { return agentBoxPrefix + skillID + "-container" }

// journalFile is <root>/<run_id>/<skill_id>.jsonl. Both ids must already have
// passed validRunPathID.
func journalFile(runID, skillID string) string {
	return runJournalRoot + "/" + runID + "/" + skillID + ".jsonl"
}

// validRunPathID reports whether id is safe as a single path segment: the
// daemon's runIDPattern, minus "." and "..". Same rule as journal.ts checkId.
func validRunPathID(id string) bool {
	return id != "." && id != ".." && runIDPattern.MatchString(id)
}

// runMemberIndex remembers which skills were provisioned under each run id,
// in provisioning order, so a skill run can still be tailed after its lease
// ends (the run registry forgets it then). In memory only: after a daemon
// restart a skill run's journal is reachable again only through a crew run
// record. Entries older than the journal retention window are dropped.
type runMemberIndex struct {
	mu      sync.Mutex
	members map[string][]string
	added   map[string]time.Time
}

func (x *runMemberIndex) add(runID, skillID string, now time.Time, retention time.Duration) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.members == nil {
		x.members, x.added = map[string][]string{}, map[string]time.Time{}
	}
	for id, t := range x.added {
		if now.Sub(t) > retention {
			delete(x.members, id)
			delete(x.added, id)
		}
	}
	for _, m := range x.members[runID] {
		if m == skillID {
			return
		}
	}
	x.members[runID] = append(x.members[runID], skillID)
	x.added[runID] = now
}

func (x *runMemberIndex) get(runID string) []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]string(nil), x.members[runID]...)
}

// runMembers resolves a run id to its member skills, entry skill first: a
// crew run from its (durable) run record, else a skill run from the index.
func (s *AgentSkillServer) runMembers(ctx context.Context, runID string) ([]string, error) {
	if s.crewRunMembers != nil {
		members, ok, err := s.crewRunMembers(ctx, runID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "look up run %q: %v", runID, err)
		}
		if ok && len(members) > 0 {
			return members, nil
		}
	}
	if members := s.runIndex.get(runID); len(members) > 0 {
		return members, nil
	}
	return nil, status.Errorf(codes.NotFound, "run %q not found", runID)
}

// boxScript returns the exec seam: the injected fake, else the container
// manager.
func (s *AgentSkillServer) boxScript() boxScriptFunc {
	if s.execScript != nil {
		return s.execScript
	}
	if s.recipes == nil || s.recipes.containers == nil || s.recipes.containers.manager == nil {
		return nil
	}
	mgr := s.recipes.containers.manager
	return func(box, script string) (string, error) {
		out, stderr, code, err := mgr.ExecWithExitCode(box, []string{"sh", "-c", script})
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("exit %d: %s", code, strings.TrimSpace(stderr))
		}
		return out, nil
	}
}

// journalWindow is one read of a journal file.
type journalWindow struct {
	exists bool
	size   int64
	// lastEnded: the file's last line is a "run ended" status line.
