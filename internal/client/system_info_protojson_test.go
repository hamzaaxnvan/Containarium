package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Numeric enums and int64s are valid ProtoJSON too; unknown fields at any
// level must not prevent an older client from reading a newer daemon.
func TestGetSystemInfoAcceptsProtoJSON(t *testing.T) {
	for _, mode := range []string{`"CPU_ADMISSION_MODE_ENFORCING"`, `3`} {
		t.Run(mode, func(t *testing.T) {
			_, c := newBodyRecorder(t, fmt.Sprintf(`{
				"futureResponseField":true,
				"info":{
					"totalMemoryBytes":34359738368,"availableMemoryBytes":17179869184,
					"totalCpus":12,"committedCpuCores":4.5,"coreCommittedCpuCores":2.25,
					"cpuAdmissionMode":%s,"cpuOvercommitFactor":2.5,
					"futureSystemField":{"value":"new"},
					"storagePools":[{"pool":"p1","driver":"STORAGE_DRIVER_ZFS",
						"usedBytes":"96","totalBytes":"100","futurePoolField":true}]
				}}`, mode))
			info, err := c.GetSystemInfo()
			if err != nil {
				t.Fatalf("GetSystemInfo: %v", err)
			}
			if info.CPUBudget == nil || info.CPUBudget.AdmissionMode != incus.CPUAdmissionEnforcing {
				t.Errorf("CPU budget not decoded: %+v", info.CPUBudget)
			}
			if len(info.StoragePools) != 1 || info.StoragePools[0].UsedBytes != 96 || info.StoragePools[0].TotalBytes != 100 {
				t.Errorf("storage pools not decoded: %+v", info.StoragePools)
			}
