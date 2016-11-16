package sshauthz

import (
	"context"
	"reflect"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func keyReq(login, fp string) *pb.AuthorizeSSHRequest {
	return &pb.AuthorizeSSHRequest{
		Login: login,
		Credential: &pb.SSHCredential{Kind: &pb.SSHCredential_PublicKey{
			PublicKey: &pb.SSHPublicKeyCredential{FingerprintSha256: fp},
		}},
	}
}

func allowAs(userID string) *pb.AuthorizeSSHResponse {
	return &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW, UserId: userID}
}

// A managed decision records (fingerprint, control-plane user id, login) so
// a later TERMINATE_USER_SESSIONS order can be resolved to local logins
// (#2602). Only an ALLOW is recorded: a denial carrying a user id names who
// tried, not whose login it is.
func TestDeciderRecordsManagedIdentities(t *testing.T) {
	ids := NewManagedIdentities()
	decide := func(req *pb.AuthorizeSSHRequest, resp *pb.AuthorizeSSHResponse) {
		t.Helper()
		d := &Decider{Local: &fakeFacts{ok: true}, Cloud: &fakeCloud{resp: resp}, Managed: true, Identities: ids}
		if _, err := d.Decide(context.Background(), req); err != nil {
			t.Fatalf("Decide: %v", err)
		}
	}

	decide(keyReq("alice", "SHA256:aaa"), allowAs("cp-user-1"))
	decide(keyReq("bob-container-alice", "SHA256:aaa"), allowAs("cp-user-1")) // same key, collaborator login
	decide(keyReq("carol", "SHA256:ccc"), allowAs("cp-user-2"))
	// Denied: never recorded, even with a user id.
	decide(keyReq("dave", "SHA256:ddd"), &pb.AuthorizeSSHResponse{
		Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_NO_ACCESS, UserId: "cp-user-1",
	})
	// Allowed without a user id: nothing to record.
	decide(keyReq("erin", "SHA256:eee"), allowAs(""))

	if got, want := ids.Logins("cp-user-1"), []string{"alice", "bob-container-alice"}; !reflect.DeepEqual(got, want) {
