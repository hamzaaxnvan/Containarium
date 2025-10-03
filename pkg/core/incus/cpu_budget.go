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
