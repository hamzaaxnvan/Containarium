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
