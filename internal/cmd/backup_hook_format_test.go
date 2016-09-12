package cmd

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2405: --hook-format is a thin wrapper that lands the typed enum on the
// request; the daemon decides what it means.
func TestBackupCreate_HookFormatFlagReachesRequest(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want pb.HookFormat
	}{
		{"", pb.HookFormat_HOOK_FORMAT_UNSPECIFIED},
		{"opaque", pb.HookFormat_HOOK_FORMAT_OPAQUE},
		{"pg_custom", pb.HookFormat_HOOK_FORMAT_PG_CUSTOM},
	} {
		t.Run("flag="+tc.flag, func(t *testing.T) {
			f := withRecordingBackupClient(t)
			resetBackupCreateFlags(t)
			backupCreateHook = "/opt/backup/db-dump.sh"
			backupCreateHookFormat = tc.flag

