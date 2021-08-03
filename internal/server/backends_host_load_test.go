package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestHostLoadFromSystemInfo_ProjectsMeasuredSample(t *testing.T) {
	sampled := time.Date(2026, 7, 25, 10, 30, 0, 0, time.UTC)
	info := &pb.SystemInfo{
		TotalCpus:            16,
		CpuLoad_1Min:         4.5,
		CpuLoad_5Min:         3.25,
		CpuLoad_15Min:        2,
		TotalMemoryBytes:     64 << 30,
		AvailableMemoryBytes: 20 << 30,
		TotalDiskBytes:       2000 << 30,
		AvailableDiskBytes:   1500 << 30,
	}

	got := hostLoadFromSystemInfo(info, sampled)
	if got == nil {
		t.Fatal("host load is nil for a usable sample")
	}
	if got.CpuLoad_1M != 4.5 || got.CpuLoad_5M != 3.25 || got.CpuLoad_15M != 2 {
		t.Errorf("load averages = %v/%v/%v, want 4.5/3.25/2", got.CpuLoad_1M, got.CpuLoad_5M, got.CpuLoad_15M)
	}
	if got.CpuCores != 16 {
		t.Errorf("CpuCores = %d, want 16 (the denominator that makes load interpretable)", got.CpuCores)
	}
	if want := int64(44 << 30); got.MemoryUsedBytes != want {
		t.Errorf("MemoryUsedBytes = %d, want %d (total - available)", got.MemoryUsedBytes, want)
	}
	if got.MemoryTotalBytes != 64<<30 {
		t.Errorf("MemoryTotalBytes = %d, want %d", got.MemoryTotalBytes, int64(64<<30))
	}
	if want := int64(500 << 30); got.DiskUsedBytes != want {
		t.Errorf("DiskUsedBytes = %d, want %d (total - available)", got.DiskUsedBytes, want)
	}
	if got.DiskTotalBytes != 2000<<30 {
		t.Errorf("DiskTotalBytes = %d, want %d", got.DiskTotalBytes, int64(2000<<30))
	}
	if got.SampledAt != "2026-07-25T10:30:00Z" {
		t.Errorf("SampledAt = %q, want RFC3339 UTC", got.SampledAt)
	}
}

// GetSystemInfo swallows a failed GetSystemResources probe and substitutes an
// empty struct, so an all-zero sample means "we couldn't measure" at least as
// often as it means "idle". Reporting it as a HostLoad of zeros would render
// a struggling host as 0% used — worse than rendering nothing.
func TestHostLoadFromSystemInfo_NilWhenNothingWasMeasured(t *testing.T) {
	cases := map[string]*pb.SystemInfo{
		"nil info":     nil,
		"failed probe": {},
		"partial probe (load but no memory figure)": {CpuLoad_1Min: 3.5, TotalCpus: 8},
	}
	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			if got := hostLoadFromSystemInfo(info, time.Now()); got != nil {
				t.Fatalf("host load = %+v, want nil so callers can tell unknown from idle", got)
			}
		})
	}
}

// available and total come from separate probes on the host, so a skewed read
// can invert them. A negative "bytes in use" in a dashboard reads as a
// product bug rather than as the bad sample it is.
func TestUsedFromTotalAndAvailable_ClampsBadSamples(t *testing.T) {
	cases := []struct {
		name             string
		total, available int64
		want             int64
	}{
