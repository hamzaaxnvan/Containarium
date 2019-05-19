package container

import (
	"testing"

	incusapi "github.com/lxc/incus/v7/shared/api"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestResolveInstanceType pins the (os_type, isolation) → Incus instance
// type table from docs/architecture/ssh-new-anonymous-box.md: UNSPECIFIED
// keeps the pre-#2196 behavior (Windows → VM, else container), VM is
// honored for any OS, and CONTAINER is refused for Windows.
func TestResolveInstanceType(t *testing.T) {
	tests := []struct {
		name      string
		osType    pb.OSType
		isolation pb.IsolationType
		want      incusapi.InstanceType
		wantErr   bool
	}{
		{"windows+unspecified → vm", pb.OSType_OS_TYPE_WINDOWS_2022, pb.IsolationType_ISOLATION_TYPE_UNSPECIFIED, incusapi.InstanceTypeVM, false},
		{"ubuntu+unspecified → container", pb.OSType_OS_TYPE_UBUNTU_2404, pb.IsolationType_ISOLATION_TYPE_UNSPECIFIED, incusapi.InstanceTypeContainer, false},
		{"no os+unspecified → container", pb.OSType_OS_TYPE_UNSPECIFIED, pb.IsolationType_ISOLATION_TYPE_UNSPECIFIED, incusapi.InstanceTypeContainer, false},
		{"ubuntu+vm → vm", pb.OSType_OS_TYPE_UBUNTU_2404, pb.IsolationType_ISOLATION_TYPE_VM, incusapi.InstanceTypeVM, false},
		{"rocky+vm → vm", pb.OSType_OS_TYPE_ROCKY_9, pb.IsolationType_ISOLATION_TYPE_VM, incusapi.InstanceTypeVM, false},
		{"ubuntu+container → container", pb.OSType_OS_TYPE_UBUNTU_2404, pb.IsolationType_ISOLATION_TYPE_CONTAINER, incusapi.InstanceTypeContainer, false},
		{"windows+vm → vm", pb.OSType_OS_TYPE_WINDOWS_2022, pb.IsolationType_ISOLATION_TYPE_VM, incusapi.InstanceTypeVM, false},
		{"windows+container → error", pb.OSType_OS_TYPE_WINDOWS_2022, pb.IsolationType_ISOLATION_TYPE_CONTAINER, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveInstanceType(tt.osType, tt.isolation)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
