package ostype

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestIsWindows(t *testing.T) {
	tests := []struct {
		name   string
		osType pb.OSType
		want   bool
	}{
		{"unspecified", pb.OSType_OS_TYPE_UNSPECIFIED, false},
		{"ubuntu", pb.OSType_OS_TYPE_UBUNTU_2404, false},
		{"rocky", pb.OSType_OS_TYPE_ROCKY_9, false},
		{"rhel", pb.OSType_OS_TYPE_RHEL_9, false},
		{"windows 2022", pb.OSType_OS_TYPE_WINDOWS_2022, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWindows(tt.osType); got != tt.want {
				t.Errorf("IsWindows(%v) = %v, want %v", tt.osType, got, tt.want)
