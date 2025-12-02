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
