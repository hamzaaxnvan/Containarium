//go:build integration && !windows

package sshauthz

import (
	"crypto/rand"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"golang.org/x/crypto/ssh"
)

// Enforce mode on the real chain: the stub daemon's DENY refuses a key that
// yaml's static authorized_keys would accept, and ALLOW still opens a session.
func TestIntegrationEnforceChain(t *testing.T) {
	c := startIntegrationChain(t, "--ssh-authz-enforce")
	denyFP := ssh.FingerprintSHA256(c.denyKey.PublicKey())

	if err := c.dial(c.allowKey); err != nil {
		t.Fatalf("allowed key refused: %v", err)
	}
	if rec := c.phase(t, sshsession.SessionPhaseOpen, 0); rec.UserID != "user-allow" || rec.AuthzDecision != pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW || !rec.Enforced {
		t.Fatalf("open 0: %+v", rec)
	}

	// A client may retry a refused key on the same connection; each refusal
	// appends a denied record, and ingest keeps one row per session. Check the
	// newest record written after each attempt.
	seen := 0
	assertDenied := func(reason pb.SSHDenyReason, fp string) {
		t.Helper()
