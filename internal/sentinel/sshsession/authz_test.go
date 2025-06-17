package sshsession

import (
	"encoding/json"
	"reflect"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
)

func TestAuthorizationMetadataOnOpenCloseAndShutdown(t *testing.T) {
	recorder := &fakeRecorder{}
	p := &Plugin{Recorder: recorder}
	conn := &libplugin.ConnMeta{UserName: "alice", UniqId: "s1", FromAddr: "203.0.113.1:1234", Metadata: map[string]string{MetaUserID: "person", MetaAuthzDecision: "DENY", MetaDenyReason: "UNKNOWN_KEY"}}
	p.Config().PipeStartCallback(conn)
	p.Config().PipeErrorCallback(conn, nil)
	conn.UniqId = "s2"
	p.Config().PipeStartCallback(conn)
	p.Shutdown()
	if len(recorder.recs) != 4 {
		t.Fatalf("records: %v", recorder.recs)
	}
	for _, rec := range recorder.recs {
		if rec.UserID != "person" || rec.AuthzDecision != pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY || rec.DenyReason != pb.SSHDenyReason_SSH_DENY_REASON_UNKNOWN_KEY || rec.Enforced {
			t.Fatalf("lost metadata: %+v", rec)
		}
		raw, err := json.Marshal(rec)
		if err != nil {
