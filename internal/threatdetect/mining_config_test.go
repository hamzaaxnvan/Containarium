package threatdetect

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/netbpf"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestDefaultMiningThresholds(t *testing.T) {
	d := DefaultMiningThresholds()
	if d.MinCores != 1.5 || d.MinFractionOfLimit != 0.75 || d.Window != 5*time.Minute || d.FlowMinAge != 3*time.Minute {
		t.Fatalf("defaults = %+v", d)
	}
	if !slices.Equal(d.StratumPorts, []uint32{3333, 4444, 5555, 7777, 14444, 14433}) {
		t.Fatalf("default stratum ports = %v", d.StratumPorts)
	}
}

func TestMiningThresholdsFromProto_ZeroFieldsFallBackToDefaults(t *testing.T) {
	got := MiningThresholdsFromProto(&pb.MiningBehaviorThresholds{MinCores: 4, Window: durationpb.New(10 * time.Minute)})
	if got.MinCores != 4 || got.Window != 10*time.Minute {
		t.Fatalf("set fields not honoured: %+v", got)
	}
	d := DefaultMiningThresholds()
	if got.MinFractionOfLimit != d.MinFractionOfLimit || got.FlowMinAge != d.FlowMinAge || !slices.Equal(got.StratumPorts, d.StratumPorts) {
		t.Fatalf("unset fields did not default: %+v", got)
	}
	if nilGot := MiningThresholdsFromProto(nil); nilGot.MinCores != d.MinCores {
		t.Fatalf("nil proto = %+v, want defaults", nilGot)
	}
}

func TestMiningThresholds_Bar(t *testing.T) {
	d := DefaultMiningThresholds()
	cases := []struct {
		limit, want float64
	}{
		{0, 1.5},  // no limit: min_cores alone
		{1, 0.75}, // 1 vCPU: 0.75 x 1
		{2, 1.5},  // tie
		{8, 1.5},  // 8 vCPU: min_cores is lower
		{0.25, 0.1875},
	}
	for _, c := range cases {
		if got := d.Bar(c.limit); got != c.want {
			t.Errorf("Bar(%v) = %v, want %v", c.limit, got, c.want)
		}
	}
}
