package server

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/modelgateway"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBoxes executes the journal scripts against in-memory files, keyed by
// box + path, the way sh would in the box.
type fakeBoxes struct {
	mu    sync.Mutex
	files map[string][]byte
	calls int
	// onRead runs before each journal read (0-based read count), so a test
	// can append between polls.
	onRead func(n int, f *fakeBoxes)
	reads  int
}

var (
	readScriptRE = regexp.MustCompile(`(?s)^f='([^']*)'.*tail -c \+(\d+) "\$f" \| head -c (\d+)`)
	existsRE     = regexp.MustCompile(`^\[ -f '([^']*)' \]`)
)

func (f *fakeBoxes) put(box, path, content string) {
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[box+":"+path] = []byte(content)
}

func (f *fakeBoxes) exec(box, script string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if m := readScriptRE.FindStringSubmatch(script); m != nil {
		if f.onRead != nil {
			f.onRead(f.reads, f)
		}
		f.reads++
		data, ok := f.files[box+":"+m[1]]
		if !ok {
			return "missing\n", nil
		}
		from, _ := strconv.Atoi(m[2])
		limit, _ := strconv.Atoi(m[3])
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		ended := 0
		if strings.Contains(lines[len(lines)-1], runEndedMarker) {
			ended = 1
		}
		var chunk []byte
		if from-1 < len(data) {
			chunk = data[from-1:]
		}
		if len(chunk) > limit {
			chunk = chunk[:limit]
		}
		return fmt.Sprintf("%d %d\n%s", len(data), ended, chunk), nil
	}
	if m := existsRE.FindStringSubmatch(script); m != nil {
		if _, ok := f.files[box+":"+m[1]]; ok {
			return "yes\n", nil
		}
		return "", nil
	}
	return "", fmt.Errorf("fakeBoxes: unexpected script %q", script)
}

const (
	jStarted   = `{"seq":1,"t":"2026-09-28T00:00:00.000Z","kind":"status","text":"run started"}` + "\n"
	jAssistant = `{"seq":2,"t":"2026-09-28T00:00:01.000Z","kind":"assistant","text":"hello"}` + "\n"
	jEnded     = `{"seq":3,"t":"2026-09-28T00:00:02.000Z","kind":"status","text":"run ended exit=0"}` + "\n"
)

func newRunLogServer(f *fakeBoxes) *AgentSkillServer {
	s := &AgentSkillServer{execScript: f.exec, runLogPoll: time.Millisecond}
	now := time.Now()
	s.runIndex.add("run-skill", "hello-agent", now, time.Hour)
	s.crewRunMembers = func(_ context.Context, id string) ([]string, bool, error) {
		if id == "run-crew" {
			return []string{"planner", "writer", "reviewer"}, true, nil
		}
		return nil, false, nil
	}
	return s
}

func TestTailRunLog(t *testing.T) {
	full := jStarted + jAssistant + jEnded
	skillPath := journalFile("run-skill", "hello-agent")
	cases := []struct {
		name       string
		files      map[string]string // box:path -> content
		onRead     func(n int, f *fakeBoxes)
		req        *pb.TailRunLogRequest
		wantCode   codes.Code
		wantChunk  string
		wantEnd    int64
		wantTrunc  bool
		wantEnded  bool
		wantSkills []string
		noExec     bool
	}{
		{
			name:      "offset 0 reads the whole small file and sees the end",
			files:     map[string]string{memberBox("hello-agent") + ":" + skillPath: full},
			req:       &pb.TailRunLogRequest{RunId: "run-skill"},
			wantChunk: full, wantEnd: int64(len(full)), wantEnded: true, wantSkills: []string{"hello-agent"},
		},
		{
			name:      "mid-file resumes at the offset",
			files:     map[string]string{memberBox("hello-agent") + ":" + skillPath: full},
			req:       &pb.TailRunLogRequest{RunId: "run-skill", StartOffset: int64(len(jStarted))},
			wantChunk: jAssistant + jEnded, wantEnd: int64(len(full)), wantEnded: true, wantSkills: []string{"hello-agent"},
		},
		{
			name:    "past EOF returns nothing and keeps the offset",
			files:   map[string]string{memberBox("hello-agent") + ":" + skillPath: jStarted},
			req:     &pb.TailRunLogRequest{RunId: "run-skill", StartOffset: 10_000},
			wantEnd: 10_000, wantSkills: []string{"hello-agent"},
		},
		{
			name:      "max_bytes truncates and withholds ended",
			files:     map[string]string{memberBox("hello-agent") + ":" + skillPath: full},
			req:       &pb.TailRunLogRequest{RunId: "run-skill", MaxBytes: 10},
			wantChunk: full[:10], wantEnd: 10, wantTrunc: true, wantSkills: []string{"hello-agent"},
		},
		{
			name:      "not ended while the last line is not run ended",
			files:     map[string]string{memberBox("hello-agent") + ":" + skillPath: jStarted + jAssistant},
			req:       &pb.TailRunLogRequest{RunId: "run-skill"},
			wantChunk: jStarted + jAssistant, wantEnd: int64(len(jStarted + jAssistant)), wantSkills: []string{"hello-agent"},
		},
		{
			name:  "follow returns early once the journal grows",
			files: map[string]string{memberBox("hello-agent") + ":" + skillPath: jStarted},
			onRead: func(n int, f *fakeBoxes) {
				if n == 2 {
					f.put(memberBox("hello-agent"), skillPath, jStarted+jAssistant)
				}
			},
			req:       &pb.TailRunLogRequest{RunId: "run-skill", StartOffset: int64(len(jStarted)), FollowSeconds: 10},
			wantChunk: jAssistant, wantEnd: int64(len(jStarted + jAssistant)), wantSkills: []string{"hello-agent"},
		},
		{
			name:     "unknown run",
			req:      &pb.TailRunLogRequest{RunId: "nope"},
			wantCode: codes.NotFound, noExec: true,
		},
		{
			name:     "skill not a member",
			req:      &pb.TailRunLogRequest{RunId: "run-crew", SkillId: "hello-agent"},
			wantCode: codes.InvalidArgument, noExec: true,
		},
		{
			name: "crew: entry skill by default, skill_ids lists members with a journal",
			files: map[string]string{
				memberBox("planner") + ":" + journalFile("run-crew", "planner"):   jStarted,
				memberBox("reviewer") + ":" + journalFile("run-crew", "reviewer"): full,
