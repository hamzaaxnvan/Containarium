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
	profileBackendSkipGPU bool
	profileBackendGet     bool
	profileBackendFormat  string
)

var backendsProfileCmd = &cobra.Command{
	Use:   "profile [backend-id]",
	Short: "Record (or read) a backend's hardware capability profile",
	Long: `Record a backend's hardware capability profile: hardware class (CPU cores +
model, GPU model via the passthrough probe, RAM/disk), a bounded CPU/memory
micro-benchmark, and region. The measured class is reconciled against the
backend's self-reported class so drift is visible.

With no backend-id it profiles the local/primary daemon's host; with a peer id
it forwards to that peer (which profiles its own host). The profile is surfaced
through 'containarium backends list'.

  --skip-gpu  skip the GPU passthrough probe (CPU-only backends; faster)
  --get       read the last-recorded profile without re-running the benchmark

Admin-only. Recording runs a short micro-benchmark and (unless --skip-gpu) a
throwaway GPU probe LXC, so it can take ~30s. See #681.

Requires --server pointing at the daemon's HTTP address.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBackendsProfile,
}

func init() {
	backendsCmd.AddCommand(backendsProfileCmd)
	backendsProfileCmd.Flags().BoolVar(&profileBackendSkipGPU, "skip-gpu", false,
		"Skip the GPU passthrough probe (CPU-only backends). Records gpu_available=false.")
	backendsProfileCmd.Flags().BoolVar(&profileBackendGet, "get", false,
		"Read the last-recorded profile instead of re-recording it.")
	backendsProfileCmd.Flags().StringVarP(&profileBackendFormat, "format", "f", "text",
		"Output format: text, json")
}

// profileBackendReq is the typed /v1/capabilities/profile request body.
type profileBackendReq struct {
	BackendID string `json:"backend_id,omitempty"`
	SkipGpu   bool   `json:"skip_gpu,omitempty"`
}

// capabilityBenchmark mirrors the proto CapabilityBenchmark wire shape.
type capabilityBenchmark struct {
	CpuOpsPerSec   int64 `json:"cpuOpsPerSec,omitempty,string"`
	MemBytesPerSec int64 `json:"memBytesPerSec,omitempty,string"`
	DurationMs     int64 `json:"durationMs,omitempty,string"`
}

// capabilityProfile mirrors the proto CapabilityProfile wire shape (camelCase
// via grpc-gateway JSON).
type capabilityProfile struct {
	CpuCores         int32                `json:"cpuCores,omitempty"`
	CpuModel         string               `json:"cpuModel,omitempty"`
	TotalMemoryBytes int64                `json:"totalMemoryBytes,omitempty,string"`
	TotalDiskBytes   int64                `json:"totalDiskBytes,omitempty,string"`
	GpuModel         string               `json:"gpuModel,omitempty"`
	GpuDriverVersion string               `json:"gpuDriverVersion,omitempty"`
	GpuAvailable     bool                 `json:"gpuAvailable,omitempty"`
	Region           string               `json:"region,omitempty"`
	ReportedClass    string               `json:"reportedClass,omitempty"`
	MeasuredClass    string               `json:"measuredClass,omitempty"`
	ClassConsistent  bool                 `json:"classConsistent,omitempty"`
	Benchmark        *capabilityBenchmark `json:"benchmark,omitempty"`
	ProfiledAt       string               `json:"profiledAt,omitempty"`
}

// profileBackendResp mirrors both ProfileBackendResponse and
// GetCapabilityProfileResponse (same shape).
type profileBackendResp struct {
	Profile   *capabilityProfile `json:"profile"`
	BackendID string             `json:"backendId"`
}

