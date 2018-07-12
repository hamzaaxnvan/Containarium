package server

import (
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Failed-authentication and ban records (#2419) land as their own audit
// actions, with the counts and ban expiry in the typed detail and the source IP
// in the row's source_ip column, and without any session identity.
func TestIngestSSHSourceRecords(t *testing.T) {
	at := time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC)
	until := at.Add(5 * time.Minute)
	tests := []struct {
		name       string
		rec        *pb.SSHSessionRecord
		wantAction string
		wantDetail SSHSessionDetail
		wantIP     string
	}{
		{
			name: "auth failures in a window",
			rec: &pb.SSHSessionRecord{SessionId: "f1", Phase: pb.SSHSessionPhase_SSH_SESSION_PHASE_AUTH_FAILED,
				OccurredAt: timestamppb.New(at), ClientIp: "203.0.113.9", FailureCount: 7, BanListener: pb.SSHBanListener_SSH_BAN_LISTENER_MAIN},
			wantAction: "ssh_auth_failed", wantIP: "203.0.113.9",
			wantDetail: SSHSessionDetail{SentinelID: "sentinel-1", FailureCount: 7, BanListener: pb.SSHBanListener_SSH_BAN_LISTENER_MAIN},
		},
		{
			name: "a ban",
			rec: &pb.SSHSessionRecord{SessionId: "b1", Phase: pb.SSHSessionPhase_SSH_SESSION_PHASE_BANNED,
				OccurredAt: timestamppb.New(at), ClientIp: "203.0.113.9", FailureCount: 100, BannedUntil: timestamppb.New(until),
				BanListener: pb.SSHBanListener_SSH_BAN_LISTENER_ANONYMOUS},
			wantAction: "ssh_banned", wantIP: "203.0.113.9",
			wantDetail: SSHSessionDetail{SentinelID: "sentinel-1", FailureCount: 100, BannedUntil: "2026-10-19T01:07:03Z", BanListener: pb.SSHBanListener_SSH_BAN_LISTENER_ANONYMOUS},
		},
		{
			name: "overflow record",
			rec: &pb.SSHSessionRecord{SessionId: "o1", Phase: pb.SSHSessionPhase_SSH_SESSION_PHASE_AUTH_FAILED,
				OccurredAt: timestamppb.New(at), SuppressedCount: 4200, BanListener: pb.SSHBanListener_SSH_BAN_LISTENER_MAIN},
			wantAction: "ssh_auth_failed", wantIP: "",
			wantDetail: SSHSessionDetail{SentinelID: "sentinel-1", SuppressedCount: 4200, BanListener: pb.SSHBanListener_SSH_BAN_LISTENER_MAIN},
