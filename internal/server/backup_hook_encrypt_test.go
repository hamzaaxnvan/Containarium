package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/backup"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #1831: a hook dump is an opaque stream. It gets its own enum value so a
// reader can tell it from Postgres (never pg_restore it) and from a record
// this build does not understand.
func TestEngineToProto_Hook(t *testing.T) {
	if got := engineToProto(backup.EngineHook); got != pb.BackupEngine_BACKUP_ENGINE_HOOK {
		t.Errorf("engineToProto(%q) = %v, want BACKUP_ENGINE_HOOK", backup.EngineHook, got)
	}
}

// The wire record must carry the #1831 facts a caller needs: that the
// stored bytes are ciphertext (and to which public recipient), and which
// in-tenant hook produced an opaque dump.
func TestRecordToProto_CarriesHookAndEncryptionFields(t *testing.T) {
	r := &backup.Record{
		ID: "alice-db-dump-20260913T000000Z", Username: "alice", Database: "db-dump",
		Engine: backup.EngineHook, Hook: "/opt/backup/db-dump.sh",
		Encrypted: true, AgeRecipient: "age1examplerecipient",
