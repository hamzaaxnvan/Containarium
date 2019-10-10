package incus

import "fmt"

// CPUAdmissionMode is the CPU overcommit admission gate's posture
// (#1029, docs/CPU-CAPACITY-ADMISSION.md) as the core packages see it. The
// wire form is the CPUAdmissionMode proto enum; this is its runtime twin so
// pkg/core stays free of pkg/pb. Zero value is "not reported", so a budget
// decoded from an older daemon never reads as a disabled gate.
type CPUAdmissionMode int

const (
	// CPUAdmissionUnknown — the daemon did not report a posture.
	CPUAdmissionUnknown CPUAdmissionMode = iota
	// CPUAdmissionDisabled — no factor configured; the gate never runs.
	CPUAdmissionDisabled
	// CPUAdmissionAdvisory — factor configured, over-ceiling creates are
	// logged and admitted.
	CPUAdmissionAdvisory
	// CPUAdmissionEnforcing — over-ceiling creates are rejected.
	CPUAdmissionEnforcing
)

func (m CPUAdmissionMode) String() string {
	switch m {
	case CPUAdmissionDisabled:
		return "disabled"
	case CPUAdmissionAdvisory:
		return "advisory"
	case CPUAdmissionEnforcing:
		return "enforcing"
	default:
		return "unknown"
	}
}

// CPUBudget is one host's CPU commitment split the way the admission gate
// reasons about it (#2284): what tenants have been promised, what the
// platform's own core-role containers have been promised, and what the
// host physically has — plus the gate's posture, so a reader can tell
// whether being over the ceiling is actually being acted on.
//
// All sums are incus.CommittedCores over limits.cpu, so an unbounded
// container counts 0 — see CommittedCores for why that is conservative.
type CPUBudget struct {
	// PhysicalCPUs is the host's logical CPU count (SMT threads included,
	// the unit limits.cpu uses). 0 means unknown — every derived number
	// then reads 0 rather than dividing by it.
	PhysicalCPUs float64
	// TenantCommittedCores is SystemInfo.committed_cpu_cores: every
	// non-core-role container's committed CPU.
	TenantCommittedCores float64
	// CoreCommittedCores is every core-role container's committed CPU —
	// the half the gate deliberately does not budget.
	CoreCommittedCores float64
	// OvercommitFactor is the gate's ceiling multiple; 0 when disabled.
	OvercommitFactor float64
	AdmissionMode    CPUAdmissionMode
}

// CoreCommittedCores sums CommittedCores over the core-role containers in
// containers — the complement of the tenant-only sum the admission gate and
// SystemInfo.committed_cpu_cores use.
func CoreCommittedCores(containers []ContainerInfo) float64 {
	var sum float64
	for i := range containers {
		if containers[i].Role.IsCoreRole() {
			sum += CommittedCores(containers[i].CPU)
		}
	}
	return sum
}

// TotalCommittedCores is tenant + core: everything the host has promised.
func (b CPUBudget) TotalCommittedCores() float64 {
	return b.TenantCommittedCores + b.CoreCommittedCores
}

// TenantRatio is tenant-committed over physical — the overcommit ratio the
// gate compares against its factor. 0 when the host's CPU count is unknown.
