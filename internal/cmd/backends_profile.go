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
