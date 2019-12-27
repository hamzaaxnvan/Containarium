package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// capacityCmd is the parent for advertising / withdrawing this backend's spare
// scheduling headroom to the control plane (#680). A box that would otherwise
// scale down can instead offer its freed headroom for control-plane-directed
// scheduling, bounded by a local policy.
var capacityCmd = &cobra.Command{
	Use:   "capacity",
	Short: "Advertise or withdraw this backend's spare scheduling capacity",
	Long: `Publish (or withdraw) this backend's spare scheduling headroom to the
control plane. The advertisement is bounded by a local policy: an
optional time window, excluded workload classes, and a safety
reservation. It is surfaced through the backend list (containarium
backends list).

Examples:
  # Advertise all available headroom (always-open window)
  containarium capacity advertise --server <host:port>

  # Advertise only 09:00-17:00, holding back 20%, excluding a class
  containarium capacity advertise --window 9-17 --reserve 0.2 \
      --exclude batch --server <host:port>

  # Withdraw (idempotent)
  containarium capacity withdraw --server <host:port>

  # Withdraw and gracefully reclaim the offered workloads within 120s
  containarium capacity withdraw --drain --server <host:port>

  # Withdraw and drain within a 30s bounded window
  containarium capacity withdraw --drain --drain-window 30 --server <host:port>

  # Read the current advertisement
  containarium capacity get --server <host:port>`,
}

var (
	capWindow      string
	capReserve     float64
	capExclude     []string
	capFormat      string
	capDrain       bool
	capDrainWindow int32
)

type capacityPolicyJSON struct {
	WindowStartHour         int32    `json:"windowStartHour,omitempty"`
	WindowEndHour           int32    `json:"windowEndHour,omitempty"`
	ExcludedWorkloadClasses []string `json:"excludedWorkloadClasses,omitempty"`
	ReserveFraction         float64  `json:"reserveFraction,omitempty"`
}

type capacityHeadroomJSON struct {
	Advertised       bool                `json:"advertised"`
	SpareCpus        int32               `json:"spareCpus,omitempty"`
	SpareMemoryBytes int64               `json:"spareMemoryBytes,omitempty"`
	SpareDiskBytes   int64               `json:"spareDiskBytes,omitempty"`
	IdleFraction     float64             `json:"idleFraction,omitempty"`
	AdvertisedAt     string              `json:"advertisedAt,omitempty"`
	Policy           *capacityPolicyJSON `json:"policy,omitempty"`
}

type capacityHeadroomResponse struct {
	Headroom *capacityHeadroomJSON `json:"headroom"`
	// Drain fields are only populated by the withdraw response when --drain was
	// requested (#682). Empty/false otherwise.
	Drained             []string          `json:"drained,omitempty"`
	ForceStopped        []string          `json:"forceStopped,omitempty"`
	DrainWindowExceeded bool              `json:"drainWindowExceeded,omitempty"`
	Failed              map[string]string `json:"failed,omitempty"`
}

func init() {
	rootCmd.AddCommand(capacityCmd)

	advertiseCmd := &cobra.Command{
		Use:   "advertise",
		Short: "Advertise spare scheduling capacity (bounded by a local policy)",
		RunE:  runCapacityAdvertise,
	}
	advertiseCmd.Flags().StringVar(&capWindow, "window", "",
		"Local-clock advertisement window as START-END hours (e.g. 9-17, or 22-6 for overnight); empty = always open")
	advertiseCmd.Flags().Float64Var(&capReserve, "reserve", 0,
		"Fraction of host resources to hold back as a safety reservation [0,1)")
	advertiseCmd.Flags().StringSliceVar(&capExclude, "exclude", nil,
		"Workload class(es) to exclude from the idle/spare computation (repeatable)")
	advertiseCmd.Flags().StringVarP(&capFormat, "format", "f", "table", "Output format: table, json")
	capacityCmd.AddCommand(advertiseCmd)

	withdrawCmd := &cobra.Command{
		Use:   "withdraw",
		Short: "Withdraw the spare-capacity advertisement (idempotent)",
		RunE:  runCapacityWithdraw,
	}
	withdrawCmd.Flags().StringVarP(&capFormat, "format", "f", "table", "Output format: table, json")
	withdrawCmd.Flags().BoolVar(&capDrain, "drain", false,
		"Also reclaim the guest workloads this backend offered, draining them gracefully within a bounded window")
	withdrawCmd.Flags().Int32Var(&capDrainWindow, "drain-window", 0,
		"Bounded wall-clock budget in seconds for the graceful drain (0 = backend default, 120s). Only honored with --drain")
	capacityCmd.AddCommand(withdrawCmd)

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Show the current spare-capacity advertisement",
		RunE:  runCapacityGet,
	}
	getCmd.Flags().StringVarP(&capFormat, "format", "f", "table", "Output format: table, json")
	capacityCmd.AddCommand(getCmd)
}

// parseWindow turns "9-17" / "22-6" into (start, end) hours. An empty string
// yields (0,0) which the daemon treats as always-open.
func parseWindow(w string) (int32, int32, error) {
	if w == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(w, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid --window %q (want START-END, e.g. 9-17)", w)
	}
	var start, end int32
	if _, err := fmt.Sscanf(parts[0], "%d", &start); err != nil {
		return 0, 0, fmt.Errorf("invalid window start %q: %w", parts[0], err)
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &end); err != nil {
		return 0, 0, fmt.Errorf("invalid window end %q: %w", parts[1], err)
	}
	if start < 0 || start > 23 || end < 0 || end > 23 {
		return 0, 0, fmt.Errorf("window hours must be 0-23, got %d-%d", start, end)
	}
	return start, end, nil
