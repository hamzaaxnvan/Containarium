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
