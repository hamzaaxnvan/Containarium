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
}

func TestGPUDeviceIdentity(t *testing.T) {
	cases := []struct {
		name   string
		device map[string]string
		want   string
	}{
		{"pci preferred over id", map[string]string{"type": "gpu", "pci": "0000:0b:00.0", "id": "0"}, "0000:0b:00.0"},
		{"pci only", map[string]string{"type": "gpu", "pci": "0000:01:00.0"}, "0000:01:00.0"},
		{"id only", map[string]string{"type": "gpu", "id": "1"}, "1"},
		{"empty pci falls back to id", map[string]string{"type": "gpu", "pci": "", "id": "2"}, "2"},
		{"pass-through-all (no pin)", map[string]string{"type": "gpu"}, "GPU"},
		{"empty values", map[string]string{"type": "gpu", "pci": "", "id": ""}, "GPU"},
	}
	for _, c := range cases {
		if got := gpuDeviceIdentity(c.device); got != c.want {
			t.Errorf("%s: gpuDeviceIdentity(%v) = %q, want %q", c.name, c.device, got, c.want)
		}
	}
}

func TestFinalizeGPUInfo(t *testing.T) {
	t.Run("sorts and sets first", func(t *testing.T) {
		info := &ContainerInfo{GPUs: []string{"0000:0b:00.0", "0000:01:00.0", "0000:05:00.0"}}
		finalizeGPUInfo(info)
		want := []string{"0000:01:00.0", "0000:05:00.0", "0000:0b:00.0"}
		if !reflect.DeepEqual(info.GPUs, want) {
			t.Errorf("GPUs = %v, want sorted %v", info.GPUs, want)
		}
		if info.GPU != "0000:01:00.0" {
			t.Errorf("GPU (first) = %q, want %q", info.GPU, want[0])
		}
	})
