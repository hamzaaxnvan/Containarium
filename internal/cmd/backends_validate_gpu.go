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
