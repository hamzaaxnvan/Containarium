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
			}
		})
	}
}

func TestImageForOSType_Windows(t *testing.T) {
	got := ImageForOSType(pb.OSType_OS_TYPE_WINDOWS_2022)
	want := "local:windows-server-2022"
	if got != want {
		t.Errorf("ImageForOSType(WINDOWS_2022) = %q, want %q", got, want)
	}
}

func TestFamilyForOSType_Windows(t *testing.T) {
	got := FamilyForOSType(pb.OSType_OS_TYPE_WINDOWS_2022)
	if got != Windows {
		t.Errorf("FamilyForOSType(WINDOWS_2022) = %q, want %q", got, Windows)
	}
}

func TestLabelValue_Windows(t *testing.T) {
	got := LabelValue(pb.OSType_OS_TYPE_WINDOWS_2022)
	want := "windows_2022"
	if got != want {
		t.Errorf("LabelValue(WINDOWS_2022) = %q, want %q", got, want)
	}
}

func TestFamilyFromLabel_Windows(t *testing.T) {
	got := FamilyFromLabel("windows_2022")
	if got != Windows {
		t.Errorf("FamilyFromLabel(\"windows_2022\") = %q, want %q", got, Windows)
	}
}

func TestOSTypeFromLabel_Windows(t *testing.T) {
	got := OSTypeFromLabel("windows_2022")
	want := pb.OSType_OS_TYPE_WINDOWS_2022
	if got != want {
		t.Errorf("OSTypeFromLabel(\"windows_2022\") = %v, want %v", got, want)
	}
}

func TestOSTypeFromString_Windows(t *testing.T) {
	tests := []struct {
		input string
		want  pb.OSType
	}{
