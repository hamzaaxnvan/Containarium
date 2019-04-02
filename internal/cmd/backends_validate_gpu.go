package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	validateGPUPci    string
	validateGPUFormat string
)

var backendsValidateGPUCmd = &cobra.Command{
	Use:   "validate-gpu [backend-id]",
	Short: "Check that GPU passthrough works inside an LXC on a backend",
	Long: `Launch a throwaway nvidia.runtime LXC on the backend, run nvidia-smi inside,
tear it down, and report whether the GPU is usable (status + model + driver).

Pre-flight before provisioning a GPU container, and a re-check after any VFIO
bind or driver upgrade. With no backend-id it validates the local/primary
daemon's host; with a peer id it forwards to that peer (which validates its own
GPU). Admin-only; the daemon creates and deletes a short-lived container, so
this can take ~30s (longer if the base image isn't cached). See #316.

Requires --server pointing at the daemon's HTTP address.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBackendsValidateGPU,
}

func init() {
	backendsCmd.AddCommand(backendsValidateGPUCmd)
	backendsValidateGPUCmd.Flags().StringVar(&validateGPUPci, "pci", "",
		"Validate a specific GPU by PCI address (e.g. 0000:01:00.0). Empty = all GPUs.")
	backendsValidateGPUCmd.Flags().StringVarP(&validateGPUFormat, "format", "f", "text",
		"Output format: text, json")
}

// validateGPUReq is the typed /v1/validate-gpu request body. snake_case tags
// match the daemon's grpc-gateway field names.
type validateGPUReq struct {
	BackendID string `json:"backend_id,omitempty"`
	Pci       string `json:"pci,omitempty"`
}

// validateGPUResp mirrors the /v1/validate-gpu (ValidateGPU) response. The
// status enum serializes as its proto name via grpc-gateway, e.g.
// "GPU_STATUS_OK".
type validateGPUResp struct {
	Status        string `json:"status"`
	GpuModel      string `json:"gpuModel"`
	DriverVersion string `json:"driverVersion"`
	Detail        string `json:"detail"`
	BackendID     string `json:"backendId"`
}

func runBackendsValidateGPU(cmd *cobra.Command, args []string) error {
	if serverAddr == "" {
		return fmt.Errorf("--server is required (the platform daemon's HTTP address, e.g. http://host:8080)")
	}
	backendID := ""
	if len(args) == 1 {
		backendID = args[0]
	}

	reqBody, _ := json.Marshal(validateGPUReq{BackendID: backendID, Pci: validateGPUPci})
	url := strings.TrimSuffix(serverAddr, "/") + "/v1/validate-gpu"

	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// Generous timeout: the daemon creates+starts+execs+deletes a container,
	// and may pull the base image on first run.
	httpClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out validateGPUResp
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("decode response: %w", err)
