package hostcheck

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Host prerequisites for the coding-CLI egress program (#2379,
// docs/architecture/coding-cli-egress-allowlist.md): a cgroup_skb/egress
// program attached to a box's cgroup needs kernel >= 6.6, a unified cgroup v2
// hierarchy, and the cgroup_skb program type. Not Required: the daemon runs
// without them; only that feature cannot be enforced.

// EgressPrereqReason names why an egress prerequisite check failed. Every
// failing check's Detail starts with "<reason>: ".
type EgressPrereqReason string

const (
	ReasonKernelTooOld     EgressPrereqReason = "kernel_too_old"
	ReasonKernelUnknown    EgressPrereqReason = "kernel_version_unknown"
	ReasonCgroupNotV2      EgressPrereqReason = "cgroup_not_v2"
	ReasonCgroupUnknown    EgressPrereqReason = "cgroup_version_unknown"
	ReasonNoCgroupSKB      EgressPrereqReason = "no_cgroup_skb"
	ReasonCgroupSKBUnknown EgressPrereqReason = "cgroup_skb_unknown"
)

// Minimum kernel for the egress program (the design's documented baseline).
const (
	egressMinKernelMajor = 6
	egressMinKernelMinor = 6
)

const (
	egressCheckKernel    = "egress: kernel >= 6.6"
	egressCheckCgroupV2  = "egress: cgroup v2"
	egressCheckCgroupSKB = "egress: cgroup_skb program type"
)

// errCgroupSKBUnsupported is what a cgroupSKB probe wraps when the kernel
// definitively lacks the program type (as opposed to "could not tell").
var errCgroupSKBUnsupported = errors.New("cgroup_skb program type not supported by this kernel")

// egressPrereqProbes are the host reads behind the checks, injectable so the
// verdict logic is table-tested without a particular kernel.
type egressPrereqProbes struct {
	kernelRelease func() (string, error) // uname -r
	cgroupV2      func() (bool, error)   // /sys/fs/cgroup is a cgroup2 mount
	cgroupSKB     func() error           // nil = supported
}

// EgressPrereqChecks runs the egress prerequisite checks on this host.
func EgressPrereqChecks() []Check { return egressPrereqChecks(defaultEgressPrereqProbes()) }

func egressPrereqChecks(p egressPrereqProbes) []Check {
	return []Check{kernelCheck(p), cgroupV2Check(p), cgroupSKBCheck(p)}
}

func failed(c Check, r EgressPrereqReason, format string, args ...any) Check {
	c.Detail = string(r) + ": " + fmt.Sprintf(format, args...)
	return c
}

func kernelCheck(p egressPrereqProbes) Check { return kernelTCXCheck(p.kernelRelease) }

// kernelTCXCheck reports the running kernel and whether it can attach TCX
// hooks (>= 6.6). A nil probe, a probe error or an unparseable release is
// "unknown", which fails the check rather than passing it.
func kernelTCXCheck(release func() (string, error)) Check {
	c := Check{Name: egressCheckKernel}
	if release == nil {
		return failed(c, ReasonKernelUnknown, "no kernel release probe")
	}
	rel, err := release()
	if err != nil {
		return failed(c, ReasonKernelUnknown, "cannot read kernel release: %v", err)
	}
	supported, known := KernelSupportsTCX(rel)
	if !known {
		return failed(c, ReasonKernelUnknown, "cannot parse kernel release %q", rel)
	}
	if !supported {
		return failed(c, ReasonKernelTooOld, "kernel %s is below %d.%d (TCX attach needs it)", strings.TrimSpace(rel), egressMinKernelMajor, egressMinKernelMinor)
	}
	c.OK, c.Detail = true, "kernel "+strings.TrimSpace(rel)
	return c
}

// EgressKernelUnsupported reports whether this host's kernel is KNOWN to be too
// old for TCX attach (< 6.6), which is what the network-policy status reports as
// the KERNEL_UNSUPPORTED uncovered path (#2972). A release that cannot be read
// or parsed (including any non-Linux host) is unknown, not unsupported: false.
func EgressKernelUnsupported() bool {
	return kernelUnsupportedFrom(defaultEgressPrereqProbes().kernelRelease)
}

func kernelUnsupportedFrom(release func() (string, error)) bool {
	if release == nil {
		return false
	}
	rel, err := release()
	if err != nil {
		return false
	}
	supported, known := KernelSupportsTCX(rel)
	return known && !supported
}

// KernelSupportsTCX reports whether a kernel release string (as printed by
// `uname -r`, e.g. "6.8.0-45-generic", "6.6.12+", "5.15.0-1050-gcp") is new
// enough for TCX attach (kernel >= 6.6). known is false when the release
// cannot be parsed; supported is then false too, and callers must treat that
// as "could not tell", not as a pass. Pure.
func KernelSupportsTCX(release string) (supported bool, known bool) {
	major, minor, ok := parseKernelRelease(release)
	if !ok {
		return false, false
	}
	return major > egressMinKernelMajor || (major == egressMinKernelMajor && minor >= egressMinKernelMinor), true
