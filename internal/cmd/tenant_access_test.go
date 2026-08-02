package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantAccessCLIPreviewApplyAndConflict(t *testing.T) {
	posts := 0
	conflict := false
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing authentication")
		}
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"revision":"` + strings.Repeat("a", 64) + `","policy":{"tenant":"team-b","egressMode":"NETWORK_POLICY_MODE_LOG_ONLY"}}`))
			return
		}
		posts++
		if conflict {
			w.WriteHeader(http.StatusConflict)
