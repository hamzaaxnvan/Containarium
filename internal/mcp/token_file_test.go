package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClient_TokenFile_ReadAtRequestTime is the headline behavior of
// the CONTAINARIUM_JWT_TOKEN_FILE feature: rewriting the file is
// enough to start using a new token, no Client / MCP-server restart.
func TestClient_TokenFile_ReadAtRequestTime(t *testing.T) {
	tmp := t.TempDir()
	tokenPath := filepath.Join(tmp, "jwt")
	if err := os.WriteFile(tokenPath, []byte("token-A"), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	var seenTokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seenTokens = append(seenTokens, strings.TrimPrefix(auth, "Bearer "))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
