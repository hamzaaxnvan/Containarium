package capabilities

import (
	"testing"
	"time"
)

func TestMeasuredClass(t *testing.T) {
	cases := []struct {
		name string
		f    HostFacts
		want string
	}{
		{"gpu beats core count", HostFacts{GPUAvailable: true, CPUCores: 4}, ClassGPU},
		{"large cpu", HostFacts{CPUCores: 32}, ClassCPULarge},
		{"exactly threshold is large", HostFacts{CPUCores: cpuLargeCoreThreshold}, ClassCPULarge},
		{"small cpu", HostFacts{CPUCores: 4}, ClassCPUSmall},
		{"zero cores small", HostFacts{}, ClassCPUSmall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MeasuredClass(tc.f); got != tc.want {
				t.Fatalf("MeasuredClass = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestComputeClassReconciliation(t *testing.T) {
	cases := []struct {
		name        string
		f           HostFacts
		wantClass   string
		wantConsist bool
	}{
		{"empty reported is consistent", HostFacts{CPUCores: 4}, ClassCPUSmall, true},
		{"exact match", HostFacts{GPUAvailable: true, ReportedClass: "gpu"}, ClassGPU, true},
		{"reported names bucket", HostFacts{GPUAvailable: true, ReportedClass: "gpu-spot"}, ClassGPU, true},
		{"case insensitive", HostFacts{CPUCores: 32, ReportedClass: "CPU-Large"}, ClassCPULarge, true},
		{"drift: reported gpu but cpu-only", HostFacts{CPUCores: 4, ReportedClass: "gpu"}, ClassCPUSmall, false},
