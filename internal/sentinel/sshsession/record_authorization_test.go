package sshsession

import (
	"bytes"
	"encoding/json"
	"testing"
)

// #2519: these JSONL-only cases do not need #2516's generated enum types.
// They cover the real recorder, not a replacement implementation. Proto
// conversion and backend ingestion are deliberately separate integration work.
func TestRecord_AuthorizationIdentitySurvivesJSONL(t *testing.T) {
	for _, tc := range []struct {
		name     string
		userID   string
		enforced bool
	}{
		{"known user audit only", "user-test", false},
		{"known user enforced", "user-test", true},
		{"unknown user audit only", "", false},
		{"unknown user enforced", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Starting at JSON catches a missing field without making the
			// test fail to compile before that field has been implemented.
			input := struct {
				SessionID  string `json:"session_id"`
				Phase      string `json:"phase"`
				OccurredAt string `json:"occurred_at"`
				Login      string `json:"login"`
				UserID     string `json:"user_id"`
