package sshauthz

import (
	"context"
	"errors"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
)

type fakeFacts struct {
	userID string
	ok     bool
	err    error
	calls  int
}

func (f *fakeFacts) Check(context.Context, *pb.AuthorizeSSHRequest) (string, bool, error) {
	f.calls++
	return f.userID, f.ok, f.err
}

type fakeCloud struct {
	resp  *pb.AuthorizeSSHResponse
	err   error
	calls int
	req   *pb.AuthorizeSSHRequest
}

func (f *fakeCloud) AuthorizeSSH(_ context.Context, req *pb.AuthorizeSSHRequest, _ ...grpc.CallOption) (*pb.AuthorizeSSHResponse, error) {
	f.calls++
	f.req = req
	return f.resp, f.err
}

func TestDeciderManagedTable(t *testing.T) {
	req := &pb.AuthorizeSSHRequest{Login: "alice", ClientIp: "192.0.2.10"}
	cloudResp := &pb.AuthorizeSSHResponse{
		Decision:   pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY,
		DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING,
		UserId:     "cloud-user",
	}
	tests := []struct {
		name       string
		facts      *fakeFacts
		cloud      *fakeCloud
		managed    bool
		wantReason pb.SSHDenyReason
		wantCloud  int
	}{
		{name: "local facts deny", facts: &fakeFacts{userID: "alice", ok: false}, cloud: &fakeCloud{resp: cloudResp}, managed: true, wantReason: pb.SSHDenyReason_SSH_DENY_REASON_NO_ACCESS, wantCloud: 0},
