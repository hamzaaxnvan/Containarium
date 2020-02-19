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
	if got := CoreCommittedCores(nil); got != 0 {
		t.Fatalf("CoreCommittedCores(nil) = %v, want 0", got)
	}
}

func TestCPUBudget_DerivedNumbers(t *testing.T) {
	b := CPUBudget{PhysicalCPUs: 8, TenantCommittedCores: 199, CoreCommittedCores: 8, OvercommitFactor: 4, AdmissionMode: CPUAdmissionAdvisory}
	if got := b.Ceiling(); got != 32 {
		t.Errorf("Ceiling = %v, want 32", got)
	}
	if got := b.TenantRatio(); got != 199.0/8 {
		t.Errorf("TenantRatio = %v, want %v", got, 199.0/8)
	}
	if got := b.TotalCommittedCores(); got != 207 {
		t.Errorf("TotalCommittedCores = %v, want 207", got)
	}
	// The operator recipe: a factor at or below (physical − core) / physical
	// leaves the platform's own cores un-overcommitted.
	if got := b.HeadroomFactor(); got != 0 {
		t.Errorf("HeadroomFactor with core == physical = %v, want 0", got)
	}
	b.CoreCommittedCores = 2
