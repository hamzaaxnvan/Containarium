package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	backendsListFormat string
)

var backendsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List backend hosts (local daemon + tunnel peers)",
	Long: `List all backend hosts registered with the platform daemon. Returns
id, type (local/tunnel), health, hostname, Incus server version, container count, live
host load (1-minute CPU load average against core count, plus memory
and disk in use), and GPU inventory per backend.

Live load is measured on each host, including tunnel-connected BYOC
hosts. A "-" means the daemon had no usable sample for that host —
unknown, not idle.

The /v1/backends endpoint is HTTP-only (not gRPC), so this command
requires --server pointing at the daemon's HTTP address.`,
	Aliases: []string{"ls"},
	RunE:    runBackendsList,
}

func init() {
	backendsCmd.AddCommand(backendsListCmd)
	backendsListCmd.Flags().StringVarP(&backendsListFormat, "format", "f", "table",
		"Output format: table, json")
}

// backendInfo mirrors the /v1/backends response shape. Kept local to
// the CLI so a server-side schema change shows up here as a decode
// failure rather than a silent field-drop.
type backendInfo struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Healthy bool   `json:"healthy"`
	Version string `json:"version,omitempty"`
	// IncusVersion is the Incus server version on the backend; empty when
	// the backend could not report it.
	IncusVersion   string       `json:"incusVersion,omitempty"`
	Hostname       string       `json:"hostname,omitempty"`
	UptimeSeconds  int64        `json:"uptimeSeconds,omitempty"`
	LastSeenAt     string       `json:"lastSeenAt,omitempty"`
	OS             string       `json:"os,omitempty"`
	ContainerCount int32        `json:"containerCount"`
	GPUs           []backendGPU `json:"gpus,omitempty"`
	// HostLoad is what the machine is actually doing right now, or nil
	// when the daemon had no usable sample. Nil is meaningful: it means
	// "unknown", not "idle".
	HostLoad *hostLoad `json:"hostLoad,omitempty"`
	// Storage is the pool backing this backend's containers and whether it
	// isolates tenant volumes, or nil when the backend reported none. Nil is
	// meaningful: "we don't know", not "isolated". See #1209 / #1206.
	Storage *backendStorage `json:"storage,omitempty"`
}

// backendStorage mirrors the BackendInfo.storage wire shape (#1209).
// Enums arrive as their protojson string names.
type backendStorage struct {
	Pool       string `json:"pool,omitempty"`
	Driver     string `json:"driver,omitempty"`
	Isolation  string `json:"isolation,omitempty"`
	DriverName string `json:"driverName,omitempty"`
}

// hostLoad mirrors the BackendInfo.host_load wire shape (cloud #966).
type hostLoad struct {
	CPULoad1m        float64 `json:"cpuLoad1m,omitempty"`
	CPULoad5m        float64 `json:"cpuLoad5m,omitempty"`
	CPULoad15m       float64 `json:"cpuLoad15m,omitempty"`
	CPUCores         int32   `json:"cpuCores,omitempty"`
	MemoryUsedBytes  int64   `json:"memoryUsedBytes,omitempty,string"`
	MemoryTotalBytes int64   `json:"memoryTotalBytes,omitempty,string"`
	DiskUsedBytes    int64   `json:"diskUsedBytes,omitempty,string"`
	DiskTotalBytes   int64   `json:"diskTotalBytes,omitempty,string"`
	SampledAt        string  `json:"sampledAt,omitempty"`
}

type backendGPU struct {
	Vendor    string `json:"vendor,omitempty"`
	ModelName string `json:"modelName,omitempty"`
	VRAMBytes int64  `json:"vramBytes,omitempty"`
}

type backendsListResponse struct {
	Backends []backendInfo `json:"backends"`
}

// fetchBackends GETs /v1/backends from the platform daemon and returns the
// decoded backend list. Shared by `backends list` and `pool list` (a pool is
// the set of backends a daemon sees) so they can't drift.
func fetchBackends() ([]backendInfo, error) {
	if serverAddr == "" {
		return nil, fmt.Errorf("--server is required (the platform daemon's HTTP address, e.g. http://host:8080)")
	}
	url := strings.TrimSuffix(serverAddr, "/") + "/v1/backends"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(body))
	}

	var parsed backendsListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return parsed.Backends, nil
}

func runBackendsList(cmd *cobra.Command, args []string) error {
	backends, err := fetchBackends()
	if err != nil {
		return err
	}

	switch backendsListFormat {
	case "json":
		out, err := json.MarshalIndent(backendsListResponse{Backends: backends}, "", "  ")
		if err != nil {
