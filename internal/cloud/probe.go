package cloud

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/policyreadiness"
	"github.com/footprintai/containarium/internal/safecast"
	"github.com/footprintai/containarium/pkg/version"
)

// DefaultStatusProbe is the daemon's host-capability probe: it self-measures
// hardware (CPU cores, RAM, disk, GPU), reports the running agent version, and
// runs the `doctor` capability self-check (the DEGRADED signal — running-as-
// root / caps / useradd probe). It satisfies StatusProbe so the actuation
// client's status loop pushes it to the cloud, making the BYO fleet view show
// real specs + headroom + health.
//
// Introspection is intentionally lightweight (runtime + /proc + statfs +
// /dev/nvidia* + nvidia-smi) so it's cheap enough to run every report cycle —
// distinct from the daemon's one-shot nvidia.runtime LXC GPU validation.
type DefaultStatusProbe struct {
	// Readiness reads the daemon's policy-store readiness (#2483); nil
	// reports none. Its results are advisory checks named "readiness: …"
	// and, like posture, never change SelfCheckOK.
	Readiness policyreadiness.CheckFunc
}

// Probe gathers the current capability snapshot. Best-effort: a field it can't
// measure on this platform is reported as zero rather than failing the whole
// report (the cloud treats zero as "not reported"). The self-check is run
// in-process; for an accurate caps/useradd result the daemon must run under
// its hardened systemd unit (same caveat as `containarium doctor`).
func (p DefaultStatusProbe) Probe(ctx context.Context) (HostStatus, error) {
	totalRAM, availRAM := readMemInfoMB()
	totalDisk, availDisk := diskGB()
	gpuCount, gpuSpec := gpuInfo()
	raw := hostcheck.Run()
	// Timestamp the start of the actual measurement, so slow probes cannot
	// make older evidence appear freshly measured at send time (#2328).
	measuredAt := time.Now()
	posture := hostcheck.RunPosture()
	st := HostStatus{
		AgentVersion:  version.GetVersion(),
		CPUCores:      safecast.I32(runtime.NumCPU()),
		TotalRAMMB:    totalRAM,
		AvailRAMMB:    availRAM,
		TotalDiskGB:   totalDisk,
		AvailDiskGB:   availDisk,
		TotalGPUCount: gpuCount,
		// No host-local in-use tracking; the cloud scheduler accounts for
		// assigned GPUs. Report all detected GPUs as available headroom.
		AvailGPUCount: gpuCount,
		GPUSpec:       gpuSpec,
	}
	populateHostChecks(&st, raw, posture, measuredAt)
	if p.Readiness != nil {
		for _, r := range policyreadiness.Run(ctx, p.Readiness) {
			st.Checks = append(st.Checks, HostCheck{Name: "readiness: " + r.Service, OK: r.Ready(), Detail: r.Detail()})
		}
	}
	return st, nil
}

// populateHostChecks retains the legacy check order/names while exposing the
// same posture measurement separately. Only capability checks drive
// SelfCheckOK; hardening and readiness remain advisory (#1103).
func populateHostChecks(st *HostStatus, raw, posture []hostcheck.Check, measuredAt time.Time) {
	st.SelfCheckOK = hostcheck.AllRequiredPass(raw)
	st.Checks = make([]HostCheck, 0, len(raw)+len(posture))
	st.Posture = nil
	for _, c := range raw {
		st.Checks = append(st.Checks, HostCheck{Name: c.WireName(), OK: c.OK, Detail: c.Detail})
	}
	if len(posture) == 0 {
		return // e.g. Windows has no posture probe; do not fabricate a pass.
	}
