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
			f := withRecordingBackupClient(t)
			resetBackupCreateFlags(t)
			backupCreateKeyMode = tc.flag

			err := runBackupCreate(backupCreateCmd, []string{"alice"})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "--key-mode") {
					t.Fatalf("err = %v, want a --key-mode usage error", err)
				}
				if f.gotCreate != nil {
					t.Error("an invalid --key-mode still reached the daemon")
				}
				return
			}
			if err != nil {
				t.Fatalf("runBackupCreate: %v", err)
			}
			if f.gotCreate == nil {
				t.Fatal("CreateBackup was not called")
			}
			if f.gotCreate.KeyMode != tc.want {
				t.Errorf("request key_mode = %v, want %v", f.gotCreate.KeyMode, tc.want)
			}
		})
	}
