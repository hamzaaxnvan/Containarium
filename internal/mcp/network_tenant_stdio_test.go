package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNativeTenantMCPStdioHelper(t *testing.T) {
	if os.Getenv("NATIVE_TENANT_MCP_FIXTURE") != "1" {
		return
	}
	srv, err := NewServer(&Config{ServerURL: os.Getenv("NATIVE_TENANT_MCP_URL"), JWTToken: "fixture-token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
}
func TestNativeTenantMCPStdioInitializeCreateAndQuery(t *testing.T) {
	creates := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing fixture authentication")
		}
