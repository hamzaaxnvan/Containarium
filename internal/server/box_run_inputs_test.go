//go:build !containarium_client

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/coderun"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// inputsRun is a running box run with an approval journal holding one open
// request, the way `agent-box approval-mcp` leaves it.
type inputsRun struct {
	s     *ContainerServer
	owner string
	run   string
	dir   string
	req   coderun.InputEvent
}

func startInputsRun(t *testing.T, expiresIn time.Duration) *inputsRun {
	t.Helper()
	s, owner, run := startLongRun(t, "{{READY}}\nsleep 60")
	s.approvalSecret = []byte("test-jwt-secret")
	s.runInputConfirmWait = 2 * time.Second
	r := &inputsRun{s: s, owner: owner, run: run.GetRunName(), dir: s.boxRunDir()}
	if err := os.MkdirAll(filepath.Join(r.dir, r.run+".answers"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r.req = coderun.InputEvent{
		Kind: coderun.KindInputRequest, T: now, RequestID: "req-0123456789abcdef",
		RequestKind: coderun.RequestKindPermission, Tool: "Bash", Prompt: "Bash: rm -rf ~/repo", Input: `{"command":"rm -rf ~/repo"}`,
		Options: []coderun.Answer{coderun.AnswerAllow, coderun.AnswerDeny}, Default: coderun.AnswerDeny,
		ExpiresAt: now.Add(expiresIn),
	}
	r.appendJournal(t, r.req)
	return r
}

func (r *inputsRun) journalPath() string { return filepath.Join(r.dir, r.run+".input.jsonl") }
func (r *inputsRun) answerPath(id string) string {
	return filepath.Join(r.dir, r.run+".answers", id)
}

func (r *inputsRun) appendJournal(t *testing.T, e coderun.InputEvent) {
	t.Helper()
	line, err := e.MarshalLine()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(r.journalPath(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(line); err != nil {
		t.Fatal(err)
	}
}

// boxApproves plays the box's approval server for r.req: it accepts only an
// answer that verifies under the box's public key for the tool call it asked, and
// journals the response, as internal/agentbox does.
func (r *inputsRun) boxApproves(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(r.answerPath(r.req.RequestID)); err == nil {
				if a, err := coderun.VerifyAnswer(pub, r.run, r.req.RequestID, coderun.RequestDigest(r.req.Tool, r.req.Input), string(b)); err == nil {
					line, _ := coderun.InputEvent{Kind: coderun.KindInputResponse, T: time.Now().UTC(), RequestID: r.req.RequestID, Answer: a, Reason: coderun.ReasonAnswered}.MarshalLine()
					f, err := os.OpenFile(r.journalPath(), os.O_WRONLY|os.O_APPEND, 0o600)
					if err == nil {
						_, _ = f.Write(line)
						_ = f.Close()
					}
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func (r *inputsRun) publicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	resp, err := r.s.GetRunApprovalKey(tenantCtx(r.owner), &pb.GetRunApprovalKeyRequest{Username: r.owner})
	if err != nil {
		t.Fatalf("GetRunApprovalKey: %v", err)
	}
	pub, err := coderun.ParseApprovalPublicKey(resp.GetPublicKey())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func (r *inputsRun) answer(a pb.RunInputAnswer, input string) (*pb.AnswerRunInputResponse, error) {
	return r.answerAs(answererCtx(r.owner), a, input)
}

func (r *inputsRun) answerAs(ctx context.Context, a pb.RunInputAnswer, input string) (*pb.AnswerRunInputResponse, error) {
	return r.s.AnswerRunInput(ctx, &pb.AnswerRunInputRequest{
		Username: r.owner, RunName: r.run, RequestId: r.req.RequestID, Answer: a,
		RequestSha256: coderun.RequestDigest(r.req.Tool, input),
	})
}

// answererCtx is a human's answering token (#3191): the box-run scope plus
// code:answer, which no credential on a box carries.
func answererCtx(name string) context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), name, []string{"user"},
		[]string{auth.ScopeSSHWrite, auth.ScopeCodeAnswer})
}

// AnswerRunInput needs code:answer on top of the box-run rule (#3191). The
// owner's own token without it is refused and writes nothing: that is the
// token an agent reads from ~/.containarium/credentials.json when its owner
// logs in from inside the box, or the "*" token a skill run is given.
func TestAnswerRunInput_RequiresTheAnswerScope(t *testing.T) {
	r := startInputsRun(t, time.Minute)
	refused := map[string]context.Context{
		"ssh:write only": auth.ContextWithTestSubjectScopes(context.Background(), r.owner, []string{"user"},
			[]string{auth.ScopeSSHWrite}),
		"unscoped token": auth.ContextWithTestSubjectScopes(context.Background(), r.owner, []string{"user"}, nil),
		"wildcard token": auth.ContextWithTestSubjectScopes(context.Background(), r.owner, []string{"user"},
			[]string{auth.ScopeWildcard}),
		"code:answer without ssh:write": auth.ContextWithTestSubjectScopes(context.Background(), r.owner, []string{"user"},
			[]string{auth.ScopeCodeAnswer}),
	}
	for name, ctx := range refused {
		_, err := r.answerAs(ctx, pb.RunInputAnswer_RUN_INPUT_ANSWER_ALLOW, r.req.Input)
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s: err = %v, want PermissionDenied", name, err)
		}
		// An upgrading user must be able to fix this from the message alone:
		// it names the missing scope and how to mint a token that has it.
		if name != "code:answer without ssh:write" {
			msg := status.Convert(err).Message()
