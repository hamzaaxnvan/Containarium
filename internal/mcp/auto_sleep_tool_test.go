package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleToggleAutoSleep_RequiresUsername — missing or empty
// username short-circuits before any HTTP call. Client URL points at
// an unreachable address; reaching it would surface a different
// error.
func TestHandleToggleAutoSleep_RequiresUsername(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "tok")
	cases := []map[string]interface{}{
		{},
		{"enabled": true},
		{"username": "", "enabled": true},
	}
	for _, args := range cases {
		_, err := handleToggleAutoSleep(client, args)
		if err == nil {
			t.Errorf("expected error for args=%v", args)
			continue
		}
		if !strings.Contains(err.Error(), "username") {
			t.Errorf("err = %v, want mention of 'username'", err)
		}
	}
}

// TestHandleToggleAutoSleep_RequiresEnabledBool — `enabled` is a
// required field; pass-through types other than bool (or absent) must
// fail before the HTTP call.
func TestHandleToggleAutoSleep_RequiresEnabledBool(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "tok")
	cases := []map[string]interface{}{
		{"username": "alice"},                    // missing
		{"username": "alice", "enabled": "true"}, // string, not bool
		{"username": "alice", "enabled": 1},      // int, not bool
	}
	for _, args := range cases {
		_, err := handleToggleAutoSleep(client, args)
		if err == nil || !strings.Contains(err.Error(), "enabled") {
			t.Errorf("args=%v: err = %v, want 'enabled' validation", args, err)
		}
	}
