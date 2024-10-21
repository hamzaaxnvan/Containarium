package cmd

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `backup create <username> --box` is the CLI face of #2801: a BOX-scope
// request with no database connection, and an explicit key mode because a
// whole-box backup is never plaintext.
func TestBackupCreate_BoxFlagBuildsBoxRequest(t *testing.T) {
	f := withRecordingBackupClient(t)
	resetBackupCreateFlags(t)
	backupCreateBox = true
	backupCreateKeyMode = "age-recipient"
	backupCreateAgeRecipient = "age1examplerecipient"
	backupCreateDest, backupCreateBucket = "gcs", "gs://example-bucket/boxes"

	if err := runBackupCreate(backupCreateCmd, []string{"alice"}); err != nil {
		t.Fatalf("runBackupCreate: %v", err)
	}
	r := f.gotCreate
	if r == nil {
		t.Fatal("CreateBackup was not called")
	}
	if r.Scope != pb.BackupScope_BACKUP_SCOPE_BOX {
		t.Errorf("scope = %v, want BOX", r.Scope)
	}
	if r.Connection != nil {
