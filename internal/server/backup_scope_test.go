package server

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/footprintai/containarium/pkg/core/backup"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// --- #2800: the whole-box scope at the server boundary ---

// testBoxRecipient is a syntactically ordinary age recipient. The BOX
// path refuses before any encryption happens, so it is never parsed.
const testBoxRecipient = "age1boxscopetestrecipient"

// boxRequest is a BOX request that passes every #2800 check: a tenant with
// an existing box, a destination, an explicit key mode and no database
// connection. Each test case breaks exactly one thing.
func boxRequest() *pb.CreateBackupRequest {
	return &pb.CreateBackupRequest{
		Username:     "alice",
		Scope:        pb.BackupScope_BACKUP_SCOPE_BOX,
		Destination:  pb.BackupDestination_BACKUP_DESTINATION_LOCAL,
		AgeRecipient: testBoxRecipient,
		KeyMode:      pb.BackupKeyMode_BACKUP_KEY_MODE_AGE_RECIPIENT,
	}
}

// A BOX request is refused before any container is touched when it
// carries database-backup fields, or when it names no encryption mode:
// plaintext is forbidden for BOX scope (decision of 2026-10-25 on #2800).
func TestCreateBackup_BoxScopeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*pb.CreateBackupRequest)
		code    codes.Code
		wantMsg string
	}{
		{
			name:    "a database connection is refused",
			mutate:  func(r *pb.CreateBackupRequest) { r.Connection = &pb.PgConnection{Database: "app"} },
			code:    codes.InvalidArgument,
			wantMsg: "connection",
		},
		{
			name:    "an empty database connection is still a connection",
			mutate:  func(r *pb.CreateBackupRequest) { r.Connection = &pb.PgConnection{} },
			code:    codes.InvalidArgument,
			wantMsg: "connection",
		},
		{
			name:    "a hook is refused",
			mutate:  func(r *pb.CreateBackupRequest) { r.Hook = "/usr/local/bin/dump" },
			code:    codes.InvalidArgument,
			wantMsg: "hook",
		},
		{
			name:    "a hook format is refused",
			mutate:  func(r *pb.CreateBackupRequest) { r.HookFormat = pb.HookFormat_HOOK_FORMAT_PG_CUSTOM },
			code:    codes.InvalidArgument,
			wantMsg: "hook",
		},
		{
			name: "no encryption mode is refused (plaintext is forbidden)",
			mutate: func(r *pb.CreateBackupRequest) {
				r.KeyMode = pb.BackupKeyMode_BACKUP_KEY_MODE_UNSPECIFIED
				r.AgeRecipient = ""
			},
			code:    codes.InvalidArgument,
			wantMsg: "key_mode",
		},
		{
			// A resolvable recipient would make the database default
			// AGE_RECIPIENT; BOX still wants the mode named, not inferred.
