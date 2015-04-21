package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const managedTestKEKID = "gcp:projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/cryptoKeyVersions/1"

// managedFakeKMS stands in for the operator's own KMS client (#2403).
type managedFakeKMS struct {
	plaintext  []byte
	err        error
	unwraps    int
	gotWrapped []byte
	gotKEKID   string
}

func (f *managedFakeKMS) Wrap(context.Context, []byte) ([]byte, string, error) {
	return nil, "", errors.New("the operator side never wraps")
}

func (f *managedFakeKMS) Unwrap(_ context.Context, wrapped []byte, kekID string) ([]byte, error) {
	f.unwraps++
	f.gotWrapped, f.gotKEKID = wrapped, kekID
	if f.err != nil {
		return nil, f.err
	}
	return f.plaintext, nil
}

// managedHostAPI is the fake host: it serves records and counts every
// mutating RPC, so a test can prove nothing reached the host.
type managedHostAPI struct {
	recordingBackupAPI
	records    []*pb.BackupRecord
	gets       int
	lists      int
	restores   int
	verifies   int
	gotVerify  *pb.VerifyBackupRequest
	verifyResp *pb.VerifyBackupResponse
}

func (f *managedHostAPI) GetBackup(id string) (*pb.BackupRecord, error) {
	f.gets++
	for _, r := range f.records {
		if r.Id == id {
			return r, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *managedHostAPI) ListBackups(username string) ([]*pb.BackupRecord, error) {
	f.lists++
	var out []*pb.BackupRecord
	for _, r := range f.records {
		if username == "" || r.Username == username {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *managedHostAPI) RestoreBackup(req *pb.RestoreBackupRequest) (*pb.RestoreBackupResponse, error) {
	f.restores++
	return f.recordingBackupAPI.RestoreBackup(req)
}

func (f *managedHostAPI) VerifyBackup(req *pb.VerifyBackupRequest) (*pb.VerifyBackupResponse, error) {
	f.verifies++
	f.gotVerify = req
	return f.verifyResp, nil
}

type managedHarness struct {
	host  *managedHostAPI
	kms   *managedFakeKMS
	dials int
	// identity is a freshly generated throwaway age identity; never real.
	identity string
}

func newManagedHarness(t *testing.T) *managedHarness {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	h := &managedHarness{identity: id.String()}
	h.kms = &managedFakeKMS{plaintext: []byte(h.identity)}
	h.host = &managedHostAPI{
		records: []*pb.BackupRecord{
			{Id: "alice-app-20261001T010000Z", Username: "alice", Database: "app", CreatedAt: "2026-10-01T01:00:00Z",
				Encrypted: true, WrappedKey: []byte("wrapped-old"), KekId: managedTestKEKID, KeyMode: pb.BackupKeyMode_BACKUP_KEY_MODE_MANAGED},
			{Id: "alice-app-20261003T010000Z", Username: "alice", Database: "app", CreatedAt: "2026-10-03T01:00:00Z",
				Encrypted: true, WrappedKey: []byte("wrapped-new"), KekId: managedTestKEKID, KeyMode: pb.BackupKeyMode_BACKUP_KEY_MODE_MANAGED},
			{Id: "alice-other-20261009T010000Z", Username: "alice", Database: "other", CreatedAt: "2026-10-09T01:00:00Z"},
			{Id: "bob-app-20261009T010000Z", Username: "bob", Database: "app", CreatedAt: "2026-10-09T01:00:00Z"},
		},
		verifyResp: &pb.VerifyBackupResponse{
			Message:      "ok",
			Verification: &pb.BackupVerification{Result: pb.VerificationResult_VERIFICATION_RESULT_PASSED},
		},
	}
	origDial, origKMS := newBackupClientFn, managedKMSLoader
	newBackupClientFn = func() (backupAPI, error) { h.dials++; return h.host, nil }
	managedKMSLoader = func(string) (secrets.KMSClient, error) { return h.kms, nil }
	t.Cleanup(func() { newBackupClientFn, managedKMSLoader = origDial, origKMS })
	resetBackupRestoreFlags(t)
	resetBackupVerifyFlags(t)
	return h
}

func resetBackupRestoreFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		backupRestoreClean, backupRestoreDatabase, backupRestoreAgeIdentityFile = false, "", ""
		backupRestoreManaged, backupRestoreLatest, backupRestoreUser, backupRestoreTarget = false, false, "", ""
	}
	reset()
	t.Cleanup(reset)
}

func resetBackupVerifyFlags(t *testing.T) {
	t.Helper()
	reset := func() {
		backupVerifyTarget, backupVerifyAgeIdentityFile = "", ""
		backupVerifyManaged, backupVerifyLatest, backupVerifyUser, backupVerifyDatabase = false, false, "", ""
	}
	reset()
	t.Cleanup(reset)
}

func TestRestoreManaged_UnwrapsThenSendsIdentityOverTLSOnly(t *testing.T) {
	t.Run("mTLS gRPC: identity unwrapped from the record and sent only in the RPC", func(t *testing.T) {
		h := newManagedHarness(t)
		setTransportGlobals(t, "host.example.com:50051", false, false)
		backupRestoreManaged = true

		if err := runBackupRestore(backupRestoreCmd, []string{"alice-app-20261001T010000Z"}); err != nil {
			t.Fatalf("runBackupRestore: %v", err)
		}
		if h.kms.unwraps != 1 || string(h.kms.gotWrapped) != "wrapped-old" || h.kms.gotKEKID != managedTestKEKID {
			t.Errorf("unwrap calls=%d wrapped=%q kek=%q, want the record's wrapped_key under its kek_id",
				h.kms.unwraps, h.kms.gotWrapped, h.kms.gotKEKID)
		}
		if h.host.restores != 1 || h.host.gotRestore.AgeIdentity != h.identity {
			t.Fatal("the unwrapped identity must be the restore request's age_identity")
		}
		if h.host.gotRestore.Id != "alice-app-20261001T010000Z" {
			t.Errorf("restored %q", h.host.gotRestore.Id)
		}
		for _, b := range h.kms.plaintext {
			if b != 0 {
				t.Fatal("the unwrapped identity buffer was not zeroed after the call")
			}
		}
	})

	for _, tc := range []struct {
		name     string
		server   string
		http     bool
