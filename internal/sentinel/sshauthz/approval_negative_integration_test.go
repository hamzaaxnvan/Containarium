//go:build integration && !windows

package sshauthz

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Negative wire tests for device approval (#3083). TestIntegrationEnforceDeviceApproval
// proves the approved path; these prove each way the approval stage must end
// in a refusal on the real sshpiperd chain, and that a key which never signed
// cannot borrow another key's approval.

const approvalURL = "https://approve.example.com/d"

func approvalPending(id string, poll time.Duration, expires time.Time) *pb.AuthorizeSSHResponse {
	return &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING,
		Approval: &pb.SSHDeviceApproval{ApprovalId: id, Code: "TEST-3083", Url: approvalURL, PollAfter: durationpb.New(poll), ExpiresAt: timestamppb.New(expires)}}
}

// approvalClient runs stock OpenSSH against the chain with deny_key. The
// askpass script appends every prompt to a file, then answers with Enter, or,
// when hold is set, blocks until the test ends so the prompt is never answered.
type approvalClient struct {
	c          *integrationChain
	promptFile string
	hold       bool
	// identities are the -i files offered in order; nil offers deny_key.
	identities []string
	agent      string // SSH_AUTH_SOCK-style agent socket; empty uses no agent
	holdFile   string
}

func (a *approvalClient) command(ctx context.Context, t *testing.T) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	path, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("needs the OpenSSH client")
	}
	dir := t.TempDir()
	a.promptFile = filepath.Join(dir, "prompt")
	askpass, hold := filepath.Join(dir, "askpass"), filepath.Join(dir, "hold")
	a.holdFile = hold
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$SSH_AUTHZ_PROMPT_FILE\"\n"
	if a.hold {
		if err := os.WriteFile(hold, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(hold) })
		script += "while [ -e \"" + hold + "\" ]; do sleep 0.05; done\nexit 1\n"
	} else {
		script += "printf '\\n'\n"
	}
	if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	host, port, _ := strings.Cut(a.c.address, ":")
	ids := a.identities
	if ids == nil {
		ids = []string{filepath.Join(a.c.dir, "deny_key")}
	}
	agent := a.agent
	if agent == "" {
