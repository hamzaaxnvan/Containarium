//go:build !containarium_client

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/footprintai/containarium/internal/storageprobe"
	"github.com/spf13/cobra"
)

var (
	storageProbeDir      string
	storageProbeOps      int
	storageProbeBaseline int
	storageProbeUnder    int
)

// storageProbeCmd groups the two halves of the contention measurement. Run
// `load` in one box and `probe` in another on the same backend; the ratio
// between a quiet probe and one taken under that load is the signal.
var storageProbeCmd = &cobra.Command{
	Use:   "storage-probe",
	Short: "Measure whether a co-tenant's writeback stalls this box's fsync",
	Long: `Measure storage contention between tenants on the same backend.

Why this exists: an idle latency check reports the opposite of the truth.
On a backend where every tenant rootfs sits on one filesystem — and so on
one journal — the affected containers measure as the FASTEST storage in
the fleet at rest, while collapsing by ~700x once a neighbour writes.

  affected tenant, idle .................... 17 ms
  physical host, same partition ............ 46 ms
  a ZFS-backed backend, busy .............. 196 ms
  affected tenant, under co-tenant load .. 11,885 ms

So a single number here is not a health signal. The ratio between a quiet
baseline and a probe taken under co-tenant load is.

Usage — run these in two different boxes on the SAME backend:

  # box A: generate co-tenant dirty pages (Ctrl-C to stop)
  containarium storage-probe load

  # box B: measure, while A runs
  containarium storage-probe probe

To get a verdict, take the quiet baseline first, then the under-load
number, and hand both to "compare":

  containarium storage-probe compare --baseline-ms 17 --under-load-ms 11885

See docs/BACKEND-STORAGE-DRIVER.md and issue #1206.`,
}

var storageProbeRunCmd = &cobra.Command{
	Use:   "probe",
