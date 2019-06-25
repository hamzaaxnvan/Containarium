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
