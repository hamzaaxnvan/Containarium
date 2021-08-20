package mcp

import (
	"encoding/json"
	"testing"
)

// #3214: create_backup's require_encryption is a thin wrapper over the same
// CreateBackupRequest.require_encryption field `backup create
// --require-encryption` sets (#3169). It is passed through unchanged;
// the daemon owns the refusal.
func TestCreateBackup_RequireEncryptionParam(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]interface{}
		want bool
	}{
		{"omitted", map[string]interface{}{}, false},
		{"false", map[string]interface{}{"require_encryption": false}, false},
		{"true", map[string]interface{}{"require_encryption": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &createBackupOnlyAPI{}
			args := map[string]interface{}{"username": "alice", "database": "app"}
			for k, v := range tc.args {
				args[k] = v
			}
			if _, err := handleCreateBackup(api, args); err != nil {
				t.Fatalf("handleCreateBackup: %v", err)
			}
			if api.got == nil || api.got.RequireEncryption != tc.want {
				t.Fatalf("request = %+v, want RequireEncryption=%v", api.got, tc.want)
			}
		})
