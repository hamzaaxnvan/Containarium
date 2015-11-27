//go:build !windows

package agentbox

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
)

// newTestApprovalServer is an approval server for run "code" in a temp dir,
// given the public key the way `code run` gives it (hex on its command line).
func newTestApprovalServer(t *testing.T, pub ed25519.PublicKey, timeout time.Duration) *approvalServer {
	t.Helper()
	dir := t.TempDir()
	s := &approvalServer{run: "code", dir: dir, timeout: timeout, now: time.Now, pollInterval: 10 * time.Millisecond}
	if err := os.MkdirAll(filepath.Join(dir, "code.answers"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.setPublicKey(hex.EncodeToString(pub))
	return s
}

func bashCall(command string) map[string]interface{} {
	return map[string]interface{}{
		"tool_name":   "Bash",
		"input":       map[string]interface{}{"command": command, "description": "test"},
		"tool_use_id": "toolu_test",
	}
}

func readJournal(t *testing.T, s *approvalServer) []coderun.InputEvent {
	t.Helper()
	b, err := os.ReadFile(s.journalPath())
	if err != nil {
		t.Fatal(err)
	}
	return coderun.ParseInputJournal(b)
}

// answerWhenAsked waits for the first input_request and drops content as its
// answer, the way an answerer on the box would.
func answerWhenAsked(t *testing.T, s *approvalServer, content func(id string, prompt []byte) string) {
	t.Helper()
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(s.journalPath()); err == nil {
				for _, e := range coderun.ParseInputJournal(b) {
					if e.Kind == coderun.KindInputRequest {
						_ = os.WriteFile(s.answerPath(e.RequestID), []byte(content(e.RequestID, coderun.RequestDigest(e.Tool, e.Input))), 0o600)
						return
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func decisionOf(t *testing.T, text string) promptDecision {
	t.Helper()
	var d promptDecision
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		t.Fatalf("reply %q is not a decision: %v", text, err)
	}
	return d
}

var testPub, testPriv, _ = ed25519.GenerateKey(rand.Reader)

func TestApprovalAllowResumesWithTheOriginalInput(t *testing.T) {
	s := newTestApprovalServer(t, testPub, 5*time.Second)
	answerWhenAsked(t, s, func(id string, p []byte) string {
		return coderun.FormatAnswer(testPriv, "code", id, p, coderun.AnswerAllow)
	})

	text, _ := callTool(t, s.handleApprove, bashCall("touch marker"))
	d := decisionOf(t, text)
	if d.Behavior != "allow" || !strings.Contains(string(d.UpdatedInput), `"command":"touch marker"`) {
		t.Fatalf("decision = %+v, want allow with the original input", d)
	}

	ev := readJournal(t, s)
	if len(ev) != 2 || ev[0].Kind != coderun.KindInputRequest || ev[1].Kind != coderun.KindInputResponse {
		t.Fatalf("journal = %+v, want request then response", ev)
	}
	req := ev[0]
	if req.RequestKind != coderun.RequestKindPermission || req.Prompt != "Bash: touch marker" || req.Default != coderun.AnswerDeny ||
		len(req.Options) != 2 || !strings.HasPrefix(req.RequestID, "req-") || req.ExpiresAt.Sub(req.T) != 5*time.Second {
		t.Errorf("request line = %+v", req)
	}
	if ev[1].RequestID != req.RequestID || ev[1].Answer != coderun.AnswerAllow || ev[1].Reason != coderun.ReasonAnswered {
		t.Errorf("response line = %+v", ev[1])
	}
}

func TestApprovalDenyReturnsARefusal(t *testing.T) {
	s := newTestApprovalServer(t, testPub, 5*time.Second)
	answerWhenAsked(t, s, func(id string, p []byte) string {
		return coderun.FormatAnswer(testPriv, "code", id, p, coderun.AnswerDeny)
	})

	text, _ := callTool(t, s.handleApprove, bashCall("rm -rf build"))
	if d := decisionOf(t, text); d.Behavior != "deny" || d.Message == "" {
		t.Fatalf("decision = %+v, want deny with a message", d)
	}
	ev := readJournal(t, s)
	if last := ev[len(ev)-1]; last.Answer != coderun.AnswerDeny || last.Reason != coderun.ReasonAnswered {
		t.Errorf("response line = %+v", last)
	}
}

// An answer signed with any key but the run's — what an agent writing the
// answer file itself would produce — is journaled and ignored, and the request
// still expires into a deny.
func TestApprovalForgedAnswerIsIgnoredAndTheRequestExpires(t *testing.T) {
	s := newTestApprovalServer(t, testPub, 300*time.Millisecond)
	_, forger, _ := ed25519.GenerateKey(rand.Reader)
	answerWhenAsked(t, s, func(id string, p []byte) string {
		return coderun.FormatAnswer(forger, "code", id, p, coderun.AnswerAllow)
	})

	text, _ := callTool(t, s.handleApprove, bashCall("curl evil"))
	if d := decisionOf(t, text); d.Behavior != "deny" || !strings.Contains(d.Message, "expired") {
		t.Fatalf("decision = %+v, want an expiry deny", d)
	}
	var notes, expired int
	for _, e := range readJournal(t, s) {
		if e.Kind == coderun.KindInputStatus && strings.Contains(e.Text, "signature does not verify") {
			notes++
		}
		if e.Kind == coderun.KindInputResponse && e.Reason == coderun.ReasonExpired && e.Answer == coderun.AnswerDeny {
			expired++
		}
