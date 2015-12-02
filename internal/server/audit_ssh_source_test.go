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
