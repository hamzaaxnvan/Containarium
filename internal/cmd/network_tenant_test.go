package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeTenantCLIRequiresDaemonBeforeLocalForce(t *testing.T) {
	oldServer, oldTenant := serverAddr, createNetworkTenant
	t.Cleanup(func() { serverAddr, createNetworkTenant = oldServer, oldTenant })
	serverAddr = ""
	createNetworkTenant = "team-a"
	if err := runCreate(createCmd, []string{"workload"}); err == nil || !strings.Contains(err.Error(), "requires a daemon") {
		t.Fatal(err)
	}
	if createCmd.Flags().Lookup("network-tenant") == nil {
		t.Fatal("CLI flag missing")
	}
}
func TestNativeTenantCLICapabilityQuery(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/network-tenants/capabilities" || r.Header.Get("Authorization") != "Bearer fixture-token" {
