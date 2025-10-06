package server

import (
	"encoding/json"
	"testing"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestIngestSSHAuthorizationAttribution(t *testing.T) {
	for n := int32(0); n <= 7; n++ {
		t.Run(pb.SSHDenyReason(n).String(), func(t *testing.T) {
			record := openRec("authz-session")
			record.UserId = "person"
			record.AuthzDecision = pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW
			if n != 0 {
				record.AuthzDecision = pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY
				record.DenyReason = pb.SSHDenyReason(n)
			}
			store := &fakeBatchLogger{}
			_, err := NewAuditServer(store).IngestSSHSessionRecords(ingestCtx(), &pb.IngestSSHSessionRecordsRequest{Records: []*pb.SSHSessionRecord{record}})
