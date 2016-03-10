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
		switch r.URL.Path {
		case "/v1/network-tenants/capabilities":
			_, _ = w.Write([]byte(`{"contract":"native-network-tenant/v1","backendScope":"local-lxc","nativeMembershipRead":true,"sharedTenantCreate":true,"atomicTenantAccess":false,"networkPolicyWrite":false}`))
		case "/v1/containers":
			creates++
			var req CreateContainerRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.NetworkTenant != "team-a" || req.TenantID != "default" {
				t.Error("create contract drift")
			}
			_, _ = w.Write([]byte(`{"container":{"name":"workload-container","username":"workload","networkTenant":"observed-team","networkTenantSource":"NETWORK_TENANT_SOURCE_NATIVE"}}`))
		case "/v1/containers/workload":
			_, _ = w.Write([]byte(`{"container":{"name":"workload-container","networkTenant":"observed-team","networkTenantSource":"NETWORK_TENANT_SOURCE_NATIVE"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeTenantMCPStdioHelper$")
	cmd.Env = append(os.Environ(), "NATIVE_TENANT_MCP_FIXTURE=1", "NATIVE_TENANT_MCP_URL="+fixture.URL, "HOME="+t.TempDir())
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
