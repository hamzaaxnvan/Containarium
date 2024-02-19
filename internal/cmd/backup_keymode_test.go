package cmd

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// --key-mode is the CLI face of #2402: parsed to the typed enum and sent
// verbatim; the daemon decides whether it can honour it. Omitted means
// UNSPECIFIED (the daemon default), never a client-side guess.
func TestBackupCreate_KeyModeFlagReachesRequest(t *testing.T) {
	for _, tc := range []struct {
		flag    string
		want    pb.BackupKeyMode
		wantErr bool
	}{
		{"", pb.BackupKeyMode_BACKUP_KEY_MODE_UNSPECIFIED, false},
		{"managed", pb.BackupKeyMode_BACKUP_KEY_MODE_MANAGED, false},
		{"both", pb.BackupKeyMode_BACKUP_KEY_MODE_BOTH, false},
		{"age-recipient", pb.BackupKeyMode_BACKUP_KEY_MODE_AGE_RECIPIENT, false},
		{"rot13", pb.BackupKeyMode_BACKUP_KEY_MODE_UNSPECIFIED, true},
	} {
		t.Run("--key-mode="+tc.flag, func(t *testing.T) {
