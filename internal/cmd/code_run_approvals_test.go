package cmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
)

// fakeApprovalSession records what the approval setup does to the box.
type fakeApprovalSession struct {
	probe  string // what the probe prints
	execs  []string
	writes map[string]struct{ content, mode string }
}

func (f *fakeApprovalSession) ShellExec(_ context.Context, command string) (string, error) {
	f.execs = append(f.execs, command)
	if strings.Contains(command, "approval-mcp --probe") {
		return f.probe, nil
	}
	return "", nil
}

func (f *fakeApprovalSession) WriteFile(_ context.Context, path, content, mode string) error {
	if f.writes == nil {
		f.writes = map[string]struct{ content, mode string }{}
	}
	f.writes[path] = struct{ content, mode string }{content, mode}
	return nil
}

func TestPrepareApprovalsClaudeWiresCommandWithTheDaemonPublicKey(t *testing.T) {
	sess := &fakeApprovalSession{probe: engine.ApprovalProbeOK + "\n"}
	eng := mustEngine(t, engine.NameClaude, engine.SecretCredential{})
	base := eng.RunCommand("p", false, false, "")
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	var diag bytes.Buffer

	got, note, err := prepareApprovals(context.Background(), sess, eng, "code", base, pub, 30*time.Minute, &diag)
	if err != nil {
		t.Fatal(err)
	}
	if note != "" {
		t.Errorf("a Claude run got a hookless note %q", note)
	}
	m := regexp.MustCompile(`--public-key ([0-9a-f]+) `).FindStringSubmatch(got)
	if m == nil || m[1] != hex.EncodeToString(pub) {
		t.Fatalf("command does not carry the daemon's public key:\n%s", got)
	}
	if want, _ := engine.WithApprovals(eng, base, "code", pub, 30*time.Minute); got != want {
		t.Errorf("command = %q, want engine.WithApprovals' %q", got, want)
	}
	// The box holds no secret, and setup changes nothing on it: the only
	// command sent is the read-only probe.
	if len(sess.writes) != 0 || len(sess.execs) != 1 || !strings.Contains(sess.execs[0], "--probe") {
		t.Errorf("setup touched the box: execs=%v writes=%v", sess.execs, sess.writes)
	}
	if !strings.Contains(diag.String(), "code answer") {
		t.Errorf("diag does not say how to answer: %q", diag.String())
	}
}

// An agent-box from #2183 verifies the older answer format, which binds no
// prompt: its probe line is refused.
func TestPrepareApprovalsRefusesAnAgentBoxWithTheOlderAnswerFormat(t *testing.T) {
	sess := &fakeApprovalSession{probe: "approval-mcp ok\n"}
	eng := mustEngine(t, engine.NameClaude, engine.SecretCredential{})
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, _, err := prepareApprovals(context.Background(), sess, eng, "code", "cmd", pub, time.Minute, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "newer agent-box") {
		t.Fatalf("err = %v, want the newer-agent-box error", err)
	}
}

// A name whose approval files would not sit plainly next to the run log is
// refused before anything on the box is touched, for every engine.
func TestPrepareApprovalsRefusesUnsafeRunNamesFirst(t *testing.T) {
	for _, n := range []engine.Name{engine.NameClaude, engine.NamePi} {
		for _, name := range []string{"sub/task", "../victim", ".."} {
			sess := &fakeApprovalSession{probe: engine.ApprovalProbeOK}
			_, _, err := prepareApprovals(context.Background(), sess, mustEngine(t, n, engine.SecretCredential{}), name, "cmd", nil, time.Minute, &bytes.Buffer{})
			if err == nil {
