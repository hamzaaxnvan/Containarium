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
