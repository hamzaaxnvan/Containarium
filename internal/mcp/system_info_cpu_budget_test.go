package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleGetSystemInfo_CPUBudget: the MCP get_system_info wrapper carries
// the SystemInfo CPU budget fields through unchanged and prints the advisory
// warning the daemon would (#2284) — a thin wrapper over the proto contract.
func TestHandleGetSystemInfo_CPUBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/system/info" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
