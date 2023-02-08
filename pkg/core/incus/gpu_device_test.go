package incus

import (
	"reflect"
	"testing"
)

func TestGPUDeviceName(t *testing.T) {
	// First GPU keeps the legacy "gpu" name (byte-identical to the
	// pre-multi-GPU config); subsequent GPUs get numbered names.
	cases := map[int]string{0: "gpu", 1: "gpu1", 2: "gpu2", 7: "gpu7"}
	for i, want := range cases {
		if got := gpuDeviceName(i); got != want {
			t.Errorf("gpuDeviceName(%d) = %q, want %q", i, got, want)
		}
	}
