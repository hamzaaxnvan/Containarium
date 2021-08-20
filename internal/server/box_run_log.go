package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/agentbox"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/collaborator"
	"github.com/footprintai/containarium/internal/logframe"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The daemon-side reader over a box's `code run` records (#2123): the
// TailRunLog contract (#2096) for the durable run records and logs agent-box
// keeps under /tmp/agent-box (#1672/#1674).
//
// It only ever reads. Records and logs keep the lifecycle agent-box gives
// them — rotated aside when a later run reuses the name, gone when /tmp is
// cleared — and nothing here writes, moves, or deletes them.
//
// The reader execs as root inside a box whose user owns those files, so it
// opens each one only if it is a regular file whose resolved path is exactly
// the one asked for: a symlink planted by the box's user (a collaborator
// without sudo, say) cannot turn it into a root read of another file.

// boxRunMaxFrameLine bounds one framed-log line: agent-box's framer reads
// 32 KiB at a time, which base64-encodes to 43692 bytes plus "<s> " and "\n".
// A framed read always asks for at least this much, so a whole frame fits.
const boxRunMaxFrameLine = 64 * 1024

// collaboratorCheckFunc reports whether subject is a collaborator on owner's
// box.
type collaboratorCheckFunc func(owner, subject string) (bool, error)

// boxRunOpenFn is the shell helper every script uses to open a box file on
// fd 3: regular file, not a symlink, and resolving to exactly the path given.
const boxRunOpenFn = `open3() {
  [ -f "$1" ] && [ ! -L "$1" ] || return 1
  command exec 3<"$1" 2>/dev/null || return 1
  if [ "$(readlink /proc/$$/fd/3)" = "$1" ] && [ -f /proc/$$/fd/3 ]; then return 0; fi
  exec 3<&-
  return 1
}
b64_3() { base64 <&3 | tr -d '\n'; exec 3<&-; }
`

// boxRunListScript prints "B <boot-id>", "P <live pids>", then one
// "R <file> <base64 record> <base64 exit sidecar | -> <base64 session id | -> <base64 result log | ->"
// line per record file. The session sidecar (#2193) is a plain-text file
// (not JSON, unlike the record/exit sidecar), so it is still read through
// open3's same symlink/regular-file guard — it lives in the same
// box-user-owned directory as everything else here.
func boxRunListScript(dir string) string {
	return fmt.Sprintf(`d=%s
%s[ -d "$d" ] || exit 0
echo "B $(cat /proc/sys/kernel/random/boot_id 2>/dev/null)"
echo "P $(ls /proc | grep -E '^[0-9]+$' | tr '\n' ' ')"
for f in "$d"/*.json; do
  open3 "$f" || continue
  r=$(b64_3)
  x=-
  if open3 "${f%%.json}.exit"; then x=$(b64_3); fi
  sid=-
  if open3 "${f%%.json}.session"; then sid=$(b64_3); fi
  res=-
  if [ "$x" != - ] && grep -q -e '--output-format json' "$f" 2>/dev/null && open3 "${f%%.json}.log"; then
    res=$(head -c %d <&3 | base64 | tr -d '\n'); exec 3<&-
  fi
  tl=-
  if open3 "${f%%.json}.tools"; then tl=$(head -c %d <&3 | base64 | tr -d '\n'); exec 3<&-; fi
  echo "R ${f##*/} ${r:--} ${x:--} ${sid:--} ${res:--} ${tl:--}"
done
`, shellSingleQuote(dir), boxRunOpenFn, boxRunResultReadLimit, boxRunToolsReadLimit)
}

// boxRunResultReadLimit caps how much of an exited structured run's log the
// list script ships back so the daemon can read the result object's
// session_id. A larger object is truncated, fails to decode, and yields no id
// (fail closed).
const boxRunResultReadLimit = 1 << 20

// structuredSessionID returns the session id from a structured run's log: the
// engine's single JSON result object, decoded as JSON, using ONLY its
// top-level "session_id" (a same-named key nested in, say, a tool input is
// never consulted). Truncated or invalid JSON, a missing or non-string id, or
// an id that is not a safe run path component yields "".
func structuredSessionID(log []byte) string {
	var res struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(log, &res); err != nil {
		return ""
	}
	if !validRunPathID(res.SessionID) {
		return ""
	}
	return res.SessionID
}

// boxRunTailScript prints the run's record ("R <base64>" or "missing"), its
// exit sidecar ("X <base64 | ->"), then the log's state — "L none",
// "L unsafe", or "L <size>" followed by up to limit bytes from offset. The
// outcome is read before the size, so a run seen as exited has a complete
// log.
func boxRunTailScript(dir, name string, offset int64, limit int) string {
	return fmt.Sprintf(`d=%s
n=%s
%sif open3 "$d/$n.json"; then echo "R $(b64_3)"; else echo missing; exit 0; fi
if open3 "$d/$n.exit"; then echo "X $(b64_3)"; else echo "X -"; fi
l="$d/$n.log"
if [ ! -e "$l" ] && [ ! -L "$l" ]; then echo "L none"; exit 0; fi
open3 "$l" || { echo "L unsafe"; exit 0; }
echo "L $(stat -L -c %%s /proc/$$/fd/3)"
tail -c +%d <&3 | head -c %d
`, shellSingleQuote(dir), shellSingleQuote(name), boxRunOpenFn, offset+1, limit)
}

// boxRunView is one record as the reader sees it.
type boxRunView struct {
	fileName  string
	record    agentbox.RunRecord
	sessionID string
	tools     []*pb.BoxRunTool
}

func decodeB64Field(s string) ([]byte, error) {
	if s == "-" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// parseBoxRunViews decodes the list script's output into its records (newest
// first), the boot id, and the set of live PIDs. A record that does not decode,
// or whose file name is not a valid run name, is skipped: one bad file must not
// hide every other run.
func parseBoxRunViews(out string) (views []boxRunView, boot string, alive map[int]bool) {
	alive = map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		kind, rest, _ := strings.Cut(line, " ")
		switch kind {
		case "B":
			boot = strings.TrimSpace(rest)
		case "P":
			for _, f := range strings.Fields(rest) {
				if pid, err := strconv.Atoi(f); err == nil {
					alive[pid] = true
				}
			}
		case "R":
			fields := strings.Fields(rest)
			if len(fields) != 6 || !agentbox.IsCurrentRecordFile(fields[0]) {
				continue
			}
			name := strings.TrimSuffix(fields[0], ".json")
			if !validRunPathID(name) {
				continue
			}
			recData, err := decodeB64Field(fields[1])
			if err != nil {
				continue
			}
			record, err := agentbox.DecodeRunRecord(recData)
			if err != nil {
				continue
			}
			sidecar, err := decodeB64Field(fields[2])
			if err != nil {
				sidecar = nil
			}
			sessionIDBytes, err := decodeB64Field(fields[3])
			if err != nil {
				sessionIDBytes = nil
			}
			sessionID := strings.TrimSpace(string(sessionIDBytes))
			// A structured run's own result object, when present and valid,
			// is authoritative over any sidecar (the engine's reported id is
			// the one to resume next).
			if resBytes, err := decodeB64Field(fields[4]); err == nil && len(resBytes) > 0 {
				if id := structuredSessionID(resBytes); id != "" {
					sessionID = id
				}
			}
			// The run wrapper's own toolchain report (#2954); absent or malformed
			// means no tools, never an error.
			var tools []*pb.BoxRunTool
			if toolBytes, err := decodeB64Field(fields[5]); err == nil && len(toolBytes) > 0 {
				tools = parseBoxRunTools(toolBytes)
			}
			views = append(views, boxRunView{
				fileName:  name,
				record:    agentbox.FoldExitSidecar(record, sidecar),
				sessionID: sessionID,
				tools:     tools,
			})
		}
	}
	sort.SliceStable(views, func(i, j int) bool {
		a, b := views[i].record.StartedAt, views[j].record.StartedAt
		if !a.Equal(b) {
			return a.After(b)
		}
		return views[i].fileName < views[j].fileName
	})
	return views, boot, alive
}

// parseBoxRunList turns the list script's output into protos, newest first.
func parseBoxRunList(out, dir string) []*pb.BoxRun {
	views, boot, alive := parseBoxRunViews(out)
	runs := make([]*pb.BoxRun, 0, len(views))
	for _, v := range views {
		outcome := agentbox.ResolveOutcome(v.record, boot, func(pid int) bool { return pid > 0 && alive[pid] })
		run := boxRunProto(v.fileName, dir, v.record, outcome)
		run.SessionId = v.sessionID
		run.Tools = v.tools
		runs = append(runs, run)
	}
	return runs
}

func boxRunProto(name, dir string, r agentbox.RunRecord, outcome agentbox.RunOutcome) *pb.BoxRun {
	run := &pb.BoxRun{
		RunName:     name,
		StartedAt:   timestamppb.New(r.StartedAt),
		LogPath:     boxRunLogPath(dir, name),
		CaptureMode: pb.CaptureMode_CAPTURE_MODE_COMBINED,
	}
	if r.CaptureMode == agentbox.CaptureFramed {
		run.CaptureMode = pb.CaptureMode_CAPTURE_MODE_FRAMED
	}
	switch outcome {
	case agentbox.RunOutcomeRunning:
		run.Outcome = pb.BoxRunOutcome_BOX_RUN_OUTCOME_RUNNING
	case agentbox.RunOutcomeExited:
		run.Outcome = pb.BoxRunOutcome_BOX_RUN_OUTCOME_EXITED
	default:
		run.Outcome = pb.BoxRunOutcome_BOX_RUN_OUTCOME_UNKNOWN
	}
	run.Model = r.Model
	if r.ExitCode != nil {
		code := int32(*r.ExitCode) // #nosec G115 -- a process exit status fits in int32
		run.ExitCode = &code
	}
	if r.FinishedAt != nil {
		run.EndedAt = timestamppb.New(*r.FinishedAt)
	}
	return run
}

// boxRunLogPath is the log the reader reads: always derived from the run
// name, never the record's own log_path, which the box's user can write.
func boxRunLogPath(dir, name string) string { return dir + "/" + name + ".log" }
