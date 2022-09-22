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
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ignored-static-token")
	c.SetTokenFile(tokenPath)

	// First request: server should see token-A.
	if _, err := c.doRequest("GET", "/anything", nil); err != nil {
		t.Fatalf("first req: %v", err)
	}

	// Rotate: overwrite the file (no Client recreated, no restart).
	if err := os.WriteFile(tokenPath, []byte("token-B\n"), 0o600); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Second request: server should see token-B with whitespace trimmed.
	if _, err := c.doRequest("GET", "/anything", nil); err != nil {
		t.Fatalf("second req: %v", err)
	}

	if len(seenTokens) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seenTokens))
	}
	if seenTokens[0] != "token-A" {
		t.Errorf("first req token = %q, want %q", seenTokens[0], "token-A")
	}
	if seenTokens[1] != "token-B" {
		t.Errorf("second req token = %q, want %q (file rotation didn't take effect)", seenTokens[1], "token-B")
	}
}

// TestClient_NoTokenFile_UsesStaticToken confirms the file path is
// strictly opt-in. With no file configured, the static token captured
// at NewClient time is used.
func TestClient_NoTokenFile_UsesStaticToken(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "static-only")
	if _, err := c.doRequest("GET", "/x", nil); err != nil {
		t.Fatalf("req: %v", err)
	}
	if seen != "static-only" {
		t.Errorf("seen = %q, want %q", seen, "static-only")
	}
}

// TestClient_TokenFile_MissingFile_PropagatesError ensures a deleted /
// unreadable token file surfaces as an actionable error to the caller,
// not a silent empty token sent to the server.
