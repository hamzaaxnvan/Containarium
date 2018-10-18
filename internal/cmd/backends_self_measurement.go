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

var selfMeasurementFormat string

var backendsSelfMeasurementCmd = &cobra.Command{
	Use:   "self-measurement [backend-id]",
	Short: "Fetch a backend's signed integrity self-measurement",
	Long: `Fetch a backend's signed integrity self-measurement: digests of the running
daemon binary, the loaded in-kernel network-policy program object(s), and the
canonical policy/config state, signed with the node's identity key (the
sentinel-issued peer leaf; TPM-backed when a TPM is present).

The daemon also emits this measurement on its heartbeat. The control plane
verifies it to detect tampering of a backend's control plane.

With no backend-id it measures the local/primary daemon's host; with a peer id
it forwards to that peer (which measures its own host).

Admin-only. See #683.

Requires --server pointing at the daemon's HTTP address.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runBackendsSelfMeasurement,
}

func init() {
	backendsCmd.AddCommand(backendsSelfMeasurementCmd)
