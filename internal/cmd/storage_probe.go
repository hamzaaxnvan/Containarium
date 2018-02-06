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
	Short: "Measure N x (4 KiB write + fsync) in this box",
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := storageprobe.Probe(storageProbeDir, storageProbeOps)
		if err != nil {
			return err
		}
		fmt.Printf("%d x (4KiB write + fsync) in %s: %d ms (%.1f ms each)\n",
			res.Ops, storageProbeDir,
			res.Total.Milliseconds(), float64(res.PerOp().Microseconds())/1000)
		fmt.Printf("\nA single number is not a health signal. Compare this against the\n")
		fmt.Printf("same measurement taken while a co-tenant generates load:\n")
		fmt.Printf("  containarium storage-probe compare --baseline-ms <quiet> --under-load-ms %d\n",
			res.Total.Milliseconds())
		return nil
	},
}

var storageProbeLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Generate co-tenant dirty pages until interrupted",
	Long: `Generate dirty pages to contend with a neighbouring tenant.

Writes at volume between syncs (64 MiB per fsync by default), because
dirty-page VOLUME is what reproduces the stall — a tight fsync loop with
small writes does not. Runs until interrupted; the scratch file is
removed on exit.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		cfg := storageprobe.DefaultLoadConfig()
