package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/backup"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #1157: engine was a string on the wire, so "postgres", "postgress" and ""
// were all equally valid values of the contract and only a driver lookup at
// dump time could tell them apart. The manifest still stores a string, so
// this boundary is where an unrecognised one has to be caught.
func TestEngineToProto(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored string
		want   pb.BackupEngine
	}{
		{"the engine we support", backup.EnginePostgres, pb.BackupEngine_BACKUP_ENGINE_POSTGRES},
		{"a record predating the enum", "", pb.BackupEngine_BACKUP_ENGINE_UNSPECIFIED},
