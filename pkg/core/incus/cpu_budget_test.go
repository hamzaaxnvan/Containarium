package incus

import (
	"strings"
	"testing"
)

// TestCoreCommittedCores pins the other half of committedTenantCores: the
// cores committed by core-role (platform) containers, which the admission
// gate and SystemInfo.committed_cpu_cores deliberately leave out (#2284).
func TestCoreCommittedCores(t *testing.T) {
	containers := []ContainerInfo{
		{Name: "alice-container", Tenant: "alice", CPU: "8"},
		{Name: "postgres", Role: RolePostgres, CPU: "2"},
		{Name: "caddy", Role: RoleCaddy, CPU: "1"},
		{Name: "controlplane", Role: RoleControlPlane, CPU: "4"},
		{Name: "bob-container", Tenant: "bob", CPU: "0-3"}, // CPU-set notation, tenant
		{Name: "legacy-core", Role: RoleSecurity, CPU: ""}, // unbounded → counts 0
	}
	if got := CoreCommittedCores(containers); got != 7 {
		t.Fatalf("CoreCommittedCores = %v, want 7 (2+1+4; tenants and unbounded excluded)", got)
	}
