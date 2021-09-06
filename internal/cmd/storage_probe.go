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

