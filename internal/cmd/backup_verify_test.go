package cmd

import (
	"errors"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeBackupAPI is a backupAPI that returns a canned verification result.
// Only VerifyBackup is exercised; the rest satisfy the interface.
type fakeBackupAPI struct {
	resp    *pb.VerifyBackupResponse
	err     error
	gotReq  *pb.VerifyBackupRequest
	closed  bool
	callCnt int
}

func (f *fakeBackupAPI) VerifyBackup(req *pb.VerifyBackupRequest) (*pb.VerifyBackupResponse, error) {
	f.gotReq = req
	f.callCnt++
	return f.resp, f.err
}

func (f *fakeBackupAPI) CreateBackup(*pb.CreateBackupRequest) (*pb.CreateBackupResponse, error) {
	return nil, errors.New("not used")
}
func (f *fakeBackupAPI) ListBackups(string) ([]*pb.BackupRecord, error) {
	return nil, errors.New("not used")
}
