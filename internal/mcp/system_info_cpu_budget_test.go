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
		// Shape as grpc-gateway emits it: camelCase, enum by name,
		// doubles as numbers.
		_, _ = w.Write([]byte(`{"info":{"hostname":"h1","os":"Ubuntu","kernelVersion":"6.8","incusVersion":"6.0",
			"containersRunning":70,"containersStopped":0,"containersTotal":70,
			"totalCpus":8,"committedCpuCores":199,"coreCommittedCpuCores":8,
			"cpuAdmissionMode":"CPU_ADMISSION_MODE_ADVISORY","cpuOvercommitFactor":4}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	resp, err := client.GetSystemInfo()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Info.TotalCpus != 8 || resp.Info.CommittedCpuCores != 199 || resp.Info.CoreCommittedCpuCores != 8 ||
		resp.Info.CpuAdmissionMode != "CPU_ADMISSION_MODE_ADVISORY" || resp.Info.CpuOvercommitFactor != 4 {
		t.Fatalf("budget fields did not round-trip: %+v", resp.Info)
	}

