package container

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// gpuValidationImage is the throwaway base image. Minimal Ubuntu; nvidia.runtime
// injects the host driver + nvidia-smi, so nothing needs installing inside.
const gpuValidationImage = "images:ubuntu/24.04"

// Readiness loop tuning. Package vars (not consts) so tests can shrink them.
var (
	gpuValidationReadyTries = 15
	gpuValidationReadyDelay = 1 * time.Second
)

// GPU validation status values (mirror the proto GPUStatus enum).
const (
	GPUStatusOK          = "ok"
	GPUStatusUnavailable = "unavailable"
)

// GPUValidationResult is the outcome of a passthrough check.
type GPUValidationResult struct {
	Status        string // GPUStatusOK | GPUStatusUnavailable
	Model         string // e.g. "NVIDIA GeForce RTX 3090"
	DriverVersion string // e.g. "570.211.01"
	Detail        string // populated when Status != ok
}

// ValidateGPU launches a throwaway nvidia.runtime LXC, attaches the GPU (a
// specific PCI address if given, else all GPUs), runs nvidia-smi inside, and
// tears the container down. Returns the parsed model + driver on success; the
// throwaway is always deleted (best-effort) once it exists, even on failure.
//
// This is the daemon-side counterpart of scripts/validate-gpu-passthrough.sh
// (hardware-verified on an RTX 3090 host): the create→nvidia.runtime→start→
// nvidia-smi sequence was confirmed live before this landed. Never returns an
