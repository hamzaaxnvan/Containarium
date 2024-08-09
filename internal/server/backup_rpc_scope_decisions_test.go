package server

import (
	"bytes"
	"context"
	"log"
	"sort"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// backupRPCDecision is the recorded scope decision for one BackupService
// RPC (#2401). Every RPC the service exposes must have an entry; adding an
// RPC without deciding its scope and tenant behavior fails the test below.
type backupRPCDecision struct {
	// scope is the scope a token must hold to call the RPC.
	scope string
	// crossTenant attempts the RPC as tenant alice against tenant bob's
	// data. It must be refused with PermissionDenied.
	crossTenant func(context.Context, *BackupServer) error
}

const bobBackupID = "bob-app-20261003T010000Z"

var backupRPCDecisions = map[string]backupRPCDecision{
	"CreateBackup": {auth.ScopeBackupsWrite, func(ctx context.Context, s *BackupServer) error {
		_, err := s.CreateBackup(ctx, &pb.CreateBackupRequest{Username: "bob"})
		return err
	}},
	"CreateBackup/BOX": {auth.ScopeBackupsWrite, func(ctx context.Context, s *BackupServer) error {
		_, err := s.CreateBackup(ctx, &pb.CreateBackupRequest{
			Username: "bob", Scope: pb.BackupScope_BACKUP_SCOPE_BOX,
			KeyMode: pb.BackupKeyMode_BACKUP_KEY_MODE_MANAGED,
		})
		return err
	}},
	// ListBackups for another tenant is rewritten to the caller's own
	// tenant rather than denied (see TestBackupScopedToken_AllowsOwnBackupsOnly),
	// so it has no denial to assert here.
	"ListBackups": {auth.ScopeBackupsRead, nil},
	"GetBackup": {auth.ScopeBackupsRead, func(ctx context.Context, s *BackupServer) error {
		_, err := s.GetBackup(ctx, &pb.GetBackupRequest{Id: bobBackupID})
		return err
	}},
