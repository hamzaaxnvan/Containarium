package server

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestMapGPUVendor(t *testing.T) {
	tests := []struct {
		input string
		want  pb.GPUVendor
	}{
		{"NVIDIA", pb.GPUVendor_GPU_VENDOR_NVIDIA},
		{"nvidia", pb.GPUVendor_GPU_VENDOR_NVIDIA},
		{"NVIDIA Corporation", pb.GPUVendor_GPU_VENDOR_NVIDIA},
		{"AMD", pb.GPUVendor_GPU_VENDOR_AMD},
		{"Advanced Micro Devices", pb.GPUVendor_GPU_VENDOR_AMD},
		{"amd", pb.GPUVendor_GPU_VENDOR_AMD},
		{"Intel", pb.GPUVendor_GPU_VENDOR_INTEL},
		{"intel", pb.GPUVendor_GPU_VENDOR_INTEL},
		{"Intel Corporation", pb.GPUVendor_GPU_VENDOR_INTEL},
		{"", pb.GPUVendor_GPU_VENDOR_UNSPECIFIED},
		{"Unknown Vendor", pb.GPUVendor_GPU_VENDOR_UNSPECIFIED},
	}
	for _, tt := range tests {
		got := mapGPUVendor(tt.input)
		if got != tt.want {
			t.Errorf("mapGPUVendor(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestMapGPUModel(t *testing.T) {
	tests := []struct {
		input string
		want  pb.GPUModel
	}{
		// NVIDIA Consumer
		{"NVIDIA GeForce RTX 4090", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_4090},
		{"GeForce RTX 4090", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_4090},
		{"RTX 4080 SUPER", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_4080},
		{"RTX 4070 Ti", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_4070_TI},
		{"GeForce RTX 4070", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_4070},
		{"GeForce RTX 3090", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_3090},
		{"GeForce RTX 3080", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_3080},
		{"GeForce RTX 5090", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_5090},
		{"GeForce RTX 5080", pb.GPUModel_GPU_MODEL_NVIDIA_RTX_5080},

