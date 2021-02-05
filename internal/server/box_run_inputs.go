package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/agentbox"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coderun"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Permission requests of a box run (#2184): the daemon half of `code pending`,
// `code answer` and `code attach`'s inline answers.
//
// A run started with `code run --approvals ask` asks through `agent-box
// approval-mcp` (#2183), which journals each request to <run>.input.jsonl and
// accepts an answer only with an Ed25519 signature under the box's approval
// public key, made for the tool call it asked. The daemon holds the private key,
// derived per box from its own secret, and signs here for an authorized
// caller; the box never holds a secret.
//
// The journal is written by the box's user, which the agent runs as, so
// everything read from it is advisory: a forged line can make a request look
// closed, show another tool call, or report a response the run never made
// (so ANSWERED is the run's report, not proof), but the box only acts on an
// answer signed for the tool call it holds in memory. Whether an answer counts
// is the box's check, not one made here.

// approvalKeyContext separates this derivation from every other use of the
// daemon secret.
const approvalKeyContext = "containarium-approval-key-v1\n"

// defaultRunInputConfirmWait bounds how long AnswerRunInput waits for the box
// to journal its response. The approval server polls every 500ms.
const defaultRunInputConfirmWait = 3 * time.Second

// maxRunInputJournal caps the journal the daemon reads (#2184 review): the
// file is the agent's to write, and is read whole, so without a cap one box
// could exhaust the daemon's memory. Requests are at most 16 KiB of input
// each, so this holds hundreds.
const maxRunInputJournal = 8 << 20

// maxRunInputEvents caps the journal lines the daemon parses (#2184 review):
// 8 MiB of tiny lines would otherwise decode into millions of events. A run
// journals about two lines per request.
const maxRunInputEvents = 20000

// runInputIDPattern is agent-box's request id: "req-" + 16 hex digits. It
// becomes a path component on the box, so nothing else is accepted.
var runInputIDPattern = regexp.MustCompile(`^req-[0-9a-f]{16}$`)

// SetApprovalKeySecret sets the daemon secret box approval keys are derived
// from (the daemon's JWT secret). Without one the approval RPCs are
// Unavailable.
func (s *ContainerServer) SetApprovalKeySecret(secret []byte) {
	s.approvalSecret = append([]byte(nil), secret...)
}

// approvalKey is username's box approval key: an Ed25519 key whose seed is
// HMAC-SHA256(secret, context || username). Rotating the secret changes every
// key, so requests open at that moment can no longer be answered and expire
// into a deny.
func (s *ContainerServer) approvalKey(username string) (ed25519.PrivateKey, error) {
	if len(s.approvalSecret) == 0 {
		return nil, status.Error(codes.Unavailable, "this daemon has no secret to sign permission answers with")
	}
	m := hmac.New(sha256.New, s.approvalSecret)
	m.Write([]byte(approvalKeyContext + username))
	return ed25519.NewKeyFromSeed(m.Sum(nil)), nil
}

func (s *ContainerServer) journalCap() int64 {
	if s.runInputJournalCap > 0 {
		return s.runInputJournalCap
	}
	return maxRunInputJournal
}

func (s *ContainerServer) confirmWait() time.Duration {
	if s.runInputConfirmWait > 0 {
		return s.runInputConfirmWait
	}
	return defaultRunInputConfirmWait
}

// GetRunApprovalKey returns the box's approval public key.
func (s *ContainerServer) GetRunApprovalKey(ctx context.Context, req *pb.GetRunApprovalKeyRequest) (*pb.GetRunApprovalKeyResponse, error) {
	if err := validateBoxRunOwner(req.GetUsername()); err != nil {
		return nil, err
	}
	if err := s.authorizeBoxRuns(ctx, req.GetUsername()); err != nil {
		return nil, err
	}
	priv, err := s.approvalKey(req.GetUsername())
	if err != nil {
		return nil, err
	}
	return &pb.GetRunApprovalKeyResponse{PublicKey: hex.EncodeToString(priv.Public().(ed25519.PublicKey))}, nil
}

// validateRunInputsRun checks (username, run_name) for the input RPCs: the
// box-run rules, plus the approval run-name rule the journal's path needs.
func validateRunInputsRun(username, name string) error {
	if err := validateBoxRunOwner(username); err != nil {
		return err
	}
	if !validRunPathID(name) || coderun.ValidApprovalRunName(name) != nil {
		return status.Errorf(codes.InvalidArgument, "run_name %q is not a run name that can have permission requests", name)
	}
	return nil
}

// runInputsJournalScript prints "J <base64 journal>", "J -" when the run has
// none, or "J big <size>" for one over max bytes, opening it through the same
// symlink guard as the run reader. The read itself is bounded to max+1
// bytes, so a file that grows after the size check (the agent can append to
// it) still yields at most that much; readRunInputs refuses a read that got
// the extra byte.
func runInputsJournalScript(dir, name string, max int64) string {
	return fmt.Sprintf(`%sif open3 %s; then
  s=$(stat -L -c %%s /proc/$$/fd/3)
  if [ "$s" -gt %d ]; then exec 3<&-; echo "J big $s"; else echo "J $(head -c %d <&3 | base64 | tr -d '\n')"; exec 3<&-; fi
else echo "J -"; fi
`, boxRunOpenFn, shellSingleQuote(dir+"/"+name+".input.jsonl"), max, max+1)
}

// runInputsState is one read of a run: whether it is running, and its journal.
type runInputsState struct {
	running bool
	events  []coderun.InputEvent
}

// readRunInputs reads the run's record and journal. NotFound when there is no
// run by that name.
func (s *ContainerServer) readRunInputs(box, username, name string) (runInputsState, error) {
	exec := s.boxRunExecFn()
	if exec == nil {
		return runInputsState{}, status.Error(codes.Unavailable, "no container backend to read the run on")
	}
	dir := s.boxRunDir()
	out, err := exec(box, boxRunListScript(dir))
	if err != nil {
		return runInputsState{}, status.Errorf(codes.Unavailable, "read run records of %q: %v", username, err)
	}
	views, boot, alive := parseBoxRunViews(out)
	var st runInputsState
	found := false
	for _, v := range views {
		if v.fileName == name {
			found = true
			st.running = agentbox.ResolveOutcome(v.record, boot, func(pid int) bool { return pid > 0 && alive[pid] }) == agentbox.RunOutcomeRunning
		}
	}
	if !found {
		return runInputsState{}, status.Errorf(codes.NotFound, "no run named %q on %q", name, username)
	}
	jout, err := exec(box, runInputsJournalScript(dir, name, s.journalCap()))
	if err != nil {
		return runInputsState{}, status.Errorf(codes.Unavailable, "read the permission requests of run %q: %v", name, err)
	}
	for _, line := range strings.Split(jout, "\n") {
		b64, ok := strings.CutPrefix(strings.TrimSpace(line), "J ")
		if !ok || b64 == "-" {
			continue
		}
		if size, big := strings.CutPrefix(b64, "big "); big {
			return runInputsState{}, status.Errorf(codes.ResourceExhausted,
				"the permission journal of run %q is %s bytes, over the %d-byte limit; it cannot be read", name, size, s.journalCap())
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return runInputsState{}, status.Errorf(codes.Internal, "decode the permission journal of run %q: %v", name, err)
		}
		if int64(len(raw)) > s.journalCap() {
			return runInputsState{}, status.Errorf(codes.ResourceExhausted,
				"the permission journal of run %q grew past the %d-byte limit while it was read; it cannot be read", name, s.journalCap())
		}
		if n := bytes.Count(raw, []byte{'\n'}); n > maxRunInputEvents {
			return runInputsState{}, status.Errorf(codes.ResourceExhausted,
				"the permission journal of run %q has %d lines, over the %d-line limit; it cannot be read", name, n, maxRunInputEvents)
		}
		st.events = coderun.ParseInputJournal(raw)
	}
	return st, nil
}

// runInputRecord is what the journal says about one request: the first
// request line for its id (a later line with the same id is the agent's, not
// the box's: the id is random and unknown before the box writes it) and the
// first response to it.
type runInputRecord struct {
	request  coderun.InputEvent
	response *coderun.InputEvent
}

func findRunInput(events []coderun.InputEvent, id string) (runInputRecord, bool) {
	var rec runInputRecord
	found := false
	for i, e := range events {
		if e.RequestID != id {
			continue
		}
		switch e.Kind {
		case coderun.KindInputRequest:
			if !found {
				rec.request, found = e, true
			}
		case coderun.KindInputResponse:
			if found && rec.response == nil {
				rec.response = &events[i]
			}
		}
	}
	return rec, found
}

func runInputAnswerProto(a coderun.Answer) pb.RunInputAnswer {
	switch a {
	case coderun.AnswerAllow:
		return pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW
	case coderun.AnswerDeny:
		return pb.RunInputAnswer_RUN_INPUT_ANSWER_DENY
	}
	return pb.RunInputAnswer_RUN_INPUT_ANSWER_UNSPECIFIED
}

func runInputCloseReasonProto(r coderun.ResponseReason) pb.RunInputCloseReason {
	switch r {
	case coderun.ReasonAnswered:
		return pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_ANSWERED
	case coderun.ReasonExpired:
		return pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_EXPIRED
	case coderun.ReasonCancelled:
		return pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_CANCELLED
	case coderun.ReasonNoKey:
		return pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_NO_KEY
	case coderun.ReasonTooLarge:
		return pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_TOO_LARGE
	}
	return pb.RunInputCloseReason_RUN_INPUT_CLOSE_REASON_UNSPECIFIED
}

func runInputProto(e coderun.InputEvent) *pb.RunInput {
	kind := pb.RunInputKind_RUN_INPUT_KIND_UNSPECIFIED
	if e.RequestKind == coderun.RequestKindPermission {
		kind = pb.RunInputKind_RUN_INPUT_KIND_PERMISSION
	}
	in := &pb.RunInput{RequestId: e.RequestID, Kind: kind, Tool: e.Tool, Prompt: e.Prompt, Input: e.Input, CreatedAt: timestamppb.New(e.T)}
	if !e.ExpiresAt.IsZero() {
		in.ExpiresAt = timestamppb.New(e.ExpiresAt)
	}
	return in
}

// ListRunInputs lists a box run's open permission requests and its counts.
