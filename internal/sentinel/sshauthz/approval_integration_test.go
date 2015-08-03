//go:build integration && !windows

package sshauthz

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Device approval on the real chain (#2526): the stub daemon answers the
// offer APPROVAL_PENDING, the first poll PENDING again and the second ALLOW. A
// stock OpenSSH client, answering the one prompt through askpass, gets
// publickey partial success, the instruction, and a session; a noninteractive
// client is denied.
func TestIntegrationEnforceDeviceApproval(t *testing.T) {
	path, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("needs the OpenSSH client")
	}
	c := startIntegrationChain(t, "--ssh-authz-enforce")
	const instruction = "Approve this device at https://approve.example.com/d, code TEST-2526"
	var offers, polls atomic.Int32
	respond := func(req *pb.AuthorizeSSHRequest) *pb.AuthorizeSSHResponse {
		switch req.GetApprovalId() {
		case "":
			offers.Add(1)
			return &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING,
				Approval: &pb.SSHDeviceApproval{ApprovalId: "appr-2526", Code: "TEST-2526", Url: "https://approve.example.com/d",
					PollAfter: durationpb.New(300 * time.Millisecond), ExpiresAt: timestamppb.New(time.Now().Add(30 * time.Second))}}
		case "appr-2526":
			if polls.Add(1) == 1 {
				return &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING}
			}
			return allowed("user-approved")
		}
		t.Errorf("unexpected approval id in %v", req)
		return &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_DENIED}
	}
	c.respond.Store(&respond)
	host, port, _ := strings.Cut(c.address, ":")
	run := func(t *testing.T, opts ...string) (stdout, stderr string, prompt []byte, err error) {
		t.Helper()
		promptFile, askpass := filepath.Join(t.TempDir(), "prompt"), filepath.Join(t.TempDir(), "askpass")
