package nodevm

import (
	"fmt"
	"strings"
	"testing"
)

func cpuSpec() Spec {
	return Spec{Name: "cpu-node", Kind: KindCPU, Pool: "cpu", CPU: 16, Memory: "64GiB", Disk: "200GiB", Sentinel: "sentinel:443"}
}
func gpuSpec() Spec {
	return Spec{Name: "gpu-node", Kind: KindGPU, Pool: "gpu", CPU: 8, Memory: "32GiB", GPUPCI: "0000:01:00.0", Sentinel: "sentinel:443"}
}

func TestValidate(t *testing.T) {
	if err := cpuSpec().Validate(); err != nil {
		t.Errorf("valid cpu spec rejected: %v", err)
	}
	if err := gpuSpec().Validate(); err != nil {
		t.Errorf("valid gpu spec rejected: %v", err)
	}
	bad := []Spec{
		{Kind: KindCPU, Pool: "cpu", CPU: 1, Memory: "1GiB", Sentinel: "s:1"},            // no name
		{Name: "n", Kind: "weird", Pool: "p", CPU: 1, Memory: "1GiB", Sentinel: "s:1"},   // bad kind
		{Name: "n", Kind: KindGPU, Pool: "gpu", CPU: 1, Memory: "1GiB", Sentinel: "s:1"}, // gpu w/o pci
		{Name: "n", Kind: KindCPU, Pool: "cpu", CPU: 0, Memory: "1GiB", Sentinel: "s:1"}, // cpu<=0
		{Name: "n", Kind: KindCPU, Pool: "cpu", CPU: 1, Sentinel: "s:1"},                 // no memory
		{Name: "n", Kind: KindCPU, Pool: "cpu", CPU: 1, Memory: "1GiB"},                  // no sentinel
		{Name: "n", Kind: KindCPU, CPU: 1, Memory: "1GiB", Sentinel: "s:1"},              // no pool
	}
	for i, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("bad spec %d should be rejected", i)
		}
	}
