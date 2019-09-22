package server

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func coreBox(name string, role incus.Role, cpu string) incus.ContainerInfo {
	return incus.ContainerInfo{Name: name, Role: role, CPU: cpu}
}

// TestCPUBudget_SplitsTenantAndCore: the host budget reports the platform's
// own core-role CPU next to tenant CPU instead of silently dropping it
// (#2284 gap 1). The tenant half must stay byte-identical to what
// committed_cpu_cores already reports.
func TestCPUBudget_SplitsTenantAndCore(t *testing.T) {
	containers := []incus.ContainerInfo{
		tenant("a", "8"), tenant("b", "4"),
		coreBox("postgres", incus.RolePostgres, "2"),
		coreBox("caddy", incus.RoleCaddy, "1"),
