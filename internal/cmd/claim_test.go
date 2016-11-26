package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testClaimToken = "v1.anon-1a2b3c4d-container.ff00.1790870400.0123456789abcdef0123456789abcdef.aa"

func TestRenderClaim(t *testing.T) {
	tests := []struct {
		name    string
		content string
		json    bool
		want    []string
		wantErr bool
	}{
		{"url", "https://cloud.example.test/claim?token=" + testClaimToken, false,
			[]string{"open this link", "https://cloud.example.test/claim?token=" + testClaimToken, "expires with the box at 2026-10-01T16:00:00Z"}, false},
		{"bare token", testClaimToken, false,
			[]string{"no claim URL configured", testClaimToken, "containarium anon claim", "2026-10-01T16:00:00Z"}, false},
		{"url json", "https://cloud.example.test/claim?token=" + testClaimToken, true,
			[]string{`"url": "https://cloud.example.test/claim?token=` + testClaimToken + `"`, `"token": "` + testClaimToken + `"`, `"expires_at": "2026-10-01T16:00:00Z"`}, false},
		{"empty", "   ", false, nil, true},
		{"not a token", "https://x/claim?token=hello", false, nil, true},
	}
	for _, tt := range tests {
