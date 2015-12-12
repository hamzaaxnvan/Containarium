//go:build !windows

package hostcheck

import (
	"fmt"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/hostharden"
)

// Host security-posture checks (#1103).
//
// The capability checks in run.go answer "can this host run workloads".
// These answer "is it safe to run them here" — a different question that
// for enterprise BYOC is the load-bearing one, because the machine is
// supplied by the customer, in the customer's account, from an image we
// did not build.
//
// Three rules govern everything in this file:
//
//  1. **Unknown is never OK.** A check that cannot measure its property
//     reports NOT-OK with a "could not determine" detail. The whole
//     premise of #1103 is that we cannot evidence a dedication claim, so
//     a check that silently passes when it learned nothing would
//     manufacture exactly the false assurance being complained about.
//     The practical effect — an ordinary unhardened host lights up with
//     warnings — is the finding, not a defect.
//
//  2. **Non-blocking.** Every posture check is Required=false, so
//     AllRequiredPass (and therefore the cloud's self_check_ok, and
//     `doctor`'s exit code) is unchanged. Whether a posture miss should
//     block enrollment is a product decision (#1103 fix 5) that this
//     code deliberately leaves open.
//
//  3. **Say what was measured, not what was hoped.** Details name the
//     evidence — the file read, the value found — so a reviewer can
//     re-derive the verdict by hand. "Not encrypted" and "could not tell
//     whether it is encrypted" are different claims and are reported
//     differently.

// posturePaths are the host files the posture checks read. Grouped into
// a struct so tests can point every check at a temp tree instead of the
// real host — a posture check that can only be tested on a correctly
// hardened machine is a posture check nobody will ever run red.
type posturePaths struct {
	procMounts        string // /proc/mounts
	sysBlock          string // /sys/block
	efiVars           string // /sys/firmware/efi/efivars
	auditdPID         string // /run/auditd.pid
	sshdConfig        string // /etc/ssh/sshd_config
	sshdConfigDir     string // /etc/ssh/sshd_config.d
	aptPeriodic       string // /etc/apt/apt.conf.d/20auto-upgrades
	tunnelUnit        string // /etc/systemd/system/containarium-tunnel.service
	tunnelUnitDropIn  string // /etc/systemd/system/containarium-tunnel.service.d
	incusDataDir      string // /var/lib/incus — the volume that holds tenant data
	recoveryDir       string // /mnt/incus-data — where containarium-recovery.yaml is written (#1154)
	cgroupSystemSlice string // /sys/fs/cgroup/system.slice — per-unit realized cpu.weight (#2284)
	imdsBlockUnit     string // /etc/systemd/system/containarium-imds-block.service (#2298)
	imdsBlockWants    string // /etc/systemd/system/multi-user.target.wants/containarium-imds-block.service — the symlink `systemctl enable` creates
	metadataDialer    func() error
	// metadataBlockProbe reports whether the bridge's metadata FORWARD rule
	// is in the kernel now; a non-nil error means it could not tell.
	metadataBlockProbe func() (present bool, detail string, err error)
	// nicGuardProbe reads the tenant-guard facts from Incus (nic_guard.go).
	nicGuardProbe NICGuardProbe
	// kernelRelease reads `uname -r`; nil means unknown (egress_prereq.go).
	kernelRelease func() (string, error)
}

func defaultPosturePaths() posturePaths {
	return posturePaths{
		procMounts:        "/proc/mounts",
		sysBlock:          "/sys/block",
		efiVars:           "/sys/firmware/efi/efivars",
		auditdPID:         "/run/auditd.pid",
		sshdConfig:        "/etc/ssh/sshd_config",
		sshdConfigDir:     "/etc/ssh/sshd_config.d",
		aptPeriodic:       "/etc/apt/apt.conf.d/20auto-upgrades",
		tunnelUnit:        "/etc/systemd/system/containarium-tunnel.service",
		tunnelUnitDropIn:  "/etc/systemd/system/containarium-tunnel.service.d",
		incusDataDir:      "/var/lib/incus",
		recoveryDir:       DefaultRecoveryDir,
		cgroupSystemSlice: DefaultCgroupSystemSlice,
		imdsBlockUnit:     hostharden.ImdsBlockUnitPath,
		imdsBlockWants:    filepath.Join("/etc/systemd/system/multi-user.target.wants", filepath.Base(hostharden.ImdsBlockUnitPath)),
		metadataDialer:    dialMetadataServer,
		metadataBlockProbe: func() (bool, string, error) {
			return hostharden.MetadataBlockPresent(hostharden.DefaultBridge)
		},
		nicGuardProbe: defaultNICGuardProbe,
		kernelRelease: defaultEgressPrereqProbes().kernelRelease,
	}
}

// RunPosture executes every host security-posture check.
//
// Separate from Run() rather than appended to it, deliberately: Run()'s
// result feeds AllRequiredPass, which drives the cloud's self_check_ok
// and the daemon's startup self-check. Folding posture into it would
// couple a hardening verdict to a liveness verdict, and any later change
// to a posture check's Required flag would silently start gating
// container creation.
func RunPosture() []Check {
	return runPosture(defaultPosturePaths())
}

func runPosture(p posturePaths) []Check {
	checks := []Check{
		diskEncryptionCheck(p),
		secureBootCheck(p),
		auditdCheck(p),
		sshdConfigCheck(p),
		unattendedUpgradesCheck(p),
		metadataReachableCheck(p),
		metadataBlockCheck(p),
		recoveryConfigDurableCheck(p),
		tunnelTokenExposedCheck(p),
		platformCPUWeightCheck(p),
		nicGuardCheck(p),
		kernelTCXCheck(p.kernelRelease),
	}
	for i := range checks {
		checks[i].Kind = KindPosture
		checks[i].Required = false
	}
	return checks
}

// --- disk encryption -------------------------------------------------

// diskEncryptionCheck reports whether the volume holding tenant data is
// backed by a dm-crypt device.
//
// Deliberately narrow about what a pass means. Guest-visible dm-crypt is
// the only encryption this host can PROVE; provider-managed at-rest
// encryption (GCP CMEK/CSEK, EBS encryption) is invisible from inside
// the guest, so its absence here is "not observable", not "absent". The
// detail says so, because on a cloud BYOC host that is the likely case
// and a reviewer must not read this as "the customer's disk is
// plaintext".
func diskEncryptionCheck(p posturePaths) Check {
	c := Check{Name: "data volume encrypted (dm-crypt)"}

	mounts, err := os.ReadFile(p.procMounts) // #nosec G304 -- package-owned constant (/proc/mounts), overridden only by tests
	if err != nil {
		c.Detail = fmt.Sprintf("could not determine: reading %s: %v", p.procMounts, err)
		return c
	}
	dev, mountPoint := deviceForPath(string(mounts), p.incusDataDir)
	if dev == "" {
		c.Detail = fmt.Sprintf("could not determine: no mount in %s covers %s", p.procMounts, p.incusDataDir)
		return c
	}

	dmName := resolveDMName(dev)
	uuid, err := os.ReadFile(filepath.Join(p.sysBlock, dmName, "dm", "uuid")) // #nosec G304,G703 -- resolveDMName Base()-bounds dmName to a single path segment so no traversal is reachable; gosec cannot see that through the call. sysBlock is a package-owned constant.
	if err != nil {
		// Not a device-mapper device at all — a plain partition. That is a
		// definite negative for guest-visible encryption, not an unknown.
		c.Detail = fmt.Sprintf("%s (mounted at %s) is not a dm-crypt device; "+
			"provider-managed encryption (e.g. CMEK) cannot be observed from inside the guest and is neither confirmed nor ruled out",
			dev, mountPoint)
		return c
	}
	if !strings.HasPrefix(strings.TrimSpace(string(uuid)), "CRYPT-") {
		c.Detail = fmt.Sprintf("%s is device-mapper but not dm-crypt (uuid %q)", dev, strings.TrimSpace(string(uuid)))
		return c
	}
	c.OK = true
	c.Detail = fmt.Sprintf("%s backing %s is dm-crypt", dev, mountPoint)
	return c
}

// deviceForPath finds the device whose mount point is the longest prefix
// of target — i.e. the filesystem that actually holds it. Longest-prefix
// matters: with both "/" and "/var/lib/incus" mounted, the naive first
// match is "/" and the check would inspect the wrong volume.
func deviceForPath(procMounts, target string) (device, mountPoint string) {
	best := -1
	for _, line := range strings.Split(procMounts, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		dev, mp := f[0], f[1]
		if !strings.HasPrefix(dev, "/dev/") {
			continue // tmpfs, proc, cgroup…
		}
		if mp != "/" && !strings.HasPrefix(target, strings.TrimSuffix(mp, "/")+"/") && target != mp {
			continue
		}
		if len(mp) > best {
			best, device, mountPoint = len(mp), dev, mp
		}
	}
	return device, mountPoint
}

// resolveDMName maps a device path from /proc/mounts to the name
// /sys/block is keyed by.
//
// Not cosmetic. /sys/block uses the KERNEL name (dm-0), while /proc/mounts
// usually carries the device-mapper ALIAS (/dev/mapper/cryptdata), a symlink
// to it. Without resolving, /sys/block/mapper/cryptdata does not exist and
// every LUKS host reports "not dm-crypt" — a false negative on exactly the
// hosts that ARE encrypted, which is the worst direction for this check to be
// wrong in. Best-effort: an unresolvable path falls through to Base and simply
// misses, i.e. no worse than not trying.
//
// Base() also bounds the result to a single path segment, so a hostile
// /proc/mounts line cannot traverse out of the sysBlock root. /proc/mounts is
// kernel-owned so that is defence in depth, but this runs as root and the
// bound is one call.
func resolveDMName(dev string) string {
	resolved := dev
	if r, err := filepath.EvalSymlinks(dev); err == nil {
		resolved = r
	}
	return filepath.Base(resolved)
}

// --- secure boot -----------------------------------------------------

// secureBootCheck reads the EFI SecureBoot variable. Absence of the
// efivars tree at all means legacy BIOS boot, which is a definite
// negative: there is no measured-boot capability to enable.
func secureBootCheck(p posturePaths) Check {
	c := Check{Name: "secure boot enabled"}

	entries, err := os.ReadDir(p.efiVars)
	if err != nil {
		c.Detail = "not an EFI system (no efivars): legacy BIOS boot has no measured-boot capability"
		return c
	}
	// The variable is SecureBoot-<vendor-guid>; the guid is not fixed.
	var name string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "SecureBoot-") {
			name = e.Name()
			break
		}
	}
	if name == "" {
		c.Detail = "could not determine: no SecureBoot-* efivar present"
		return c
	}
	// Base() bounds the ReadDir-derived name to one segment.
	raw, err := os.ReadFile(filepath.Join(p.efiVars, filepath.Base(name))) // #nosec G304 -- efiVars is a package-owned constant; name is Base()-bounded
	if err != nil {
		c.Detail = fmt.Sprintf("could not determine: reading %s: %v", name, err)
		return c
	}
	on, ok := parseSecureBootVar(raw)
	if !ok {
		c.Detail = fmt.Sprintf("could not determine: %s is %d bytes, want 5", name, len(raw))
		return c
	}
	if !on {
		c.Detail = "SecureBoot efivar is 0 (disabled)"
		return c
	}
	c.OK = true
	c.Detail = "SecureBoot efivar is 1"
	return c
}

// parseSecureBootVar decodes an efivars blob: 4 bytes of attributes
// followed by the value. ok=false if it isn't that shape.
func parseSecureBootVar(raw []byte) (enabled, ok bool) {
	if len(raw) != 5 {
		return false, false
	}
	return raw[4] == 1, true
}

// --- auditd ----------------------------------------------------------

// auditdCheck looks for auditd's pidfile. Pidfile rather than shelling
// out to systemctl: the daemon may run where systemd isn't reachable,
// and a missing systemctl would report as "unknown" on a host that is
// in fact running auditd.
func auditdCheck(p posturePaths) Check {
	c := Check{Name: "auditd running"}
	data, err := os.ReadFile(p.auditdPID) // #nosec G304 -- package-owned constant (/run/auditd.pid), overridden only by tests
	if err != nil {
		if os.IsNotExist(err) {
			c.Detail = "no " + p.auditdPID + ": auditd is not running, so host-level audit trail is absent"
			return c
		}
		c.Detail = fmt.Sprintf("could not determine: reading %s: %v", p.auditdPID, err)
		return c
	}
	if strings.TrimSpace(string(data)) == "" {
		c.Detail = "could not determine: " + p.auditdPID + " is empty"
		return c
	}
	c.OK = true
	c.Detail = "auditd pidfile present (pid " + strings.TrimSpace(string(data)) + ")"
	return c
}

// --- sshd ------------------------------------------------------------

// sshdConfigCheck requires BOTH interactive-SSH constraints: root login
// off and password auth off. One without the other is not a pass — a box
// that permits passwords is brute-forceable whether or not root is
// reachable directly.
func sshdConfigCheck(p posturePaths) Check {
	c := Check{Name: "sshd hardened (no root login, no password auth)"}

	merged, err := readSSHDConfig(p.sshdConfig, p.sshdConfigDir)
	if err != nil {
		c.Detail = fmt.Sprintf("could not determine: %v", err)
		return c
	}

	rootLogin := sshdDirective(merged, "PermitRootLogin", "prohibit-password")
	passwordAuth := sshdDirective(merged, "PasswordAuthentication", "yes")

	var bad []string
	// "yes" is the only outright-permissive PermitRootLogin. The default
	// (prohibit-password) allows key-based root, which is normal for
	// cloud images and not what this check is about.
	if strings.EqualFold(rootLogin, "yes") {
		bad = append(bad, "PermitRootLogin="+rootLogin)
	}
	if !strings.EqualFold(passwordAuth, "no") {
		bad = append(bad, "PasswordAuthentication="+passwordAuth)
	}
	if len(bad) > 0 {
		c.Detail = strings.Join(bad, ", ")
		return c
	}
	c.OK = true
	c.Detail = fmt.Sprintf("PermitRootLogin=%s, PasswordAuthentication=%s", rootLogin, passwordAuth)
	return c
}

// readSSHDConfig gathers the main config and its *.conf drop-ins and hands
// them to sshdpolicy.Merge, which orders them the way sshd reads them
// (drop-ins first, lexically, then the main file — the Include sits at the
// top of a modern sshd_config). One parser is shared with the per-box
// posture reconciler (#2424) so host and box checks cannot disagree.
func readSSHDConfig(mainPath, dropInDir string) (string, error) {
	main, err := os.ReadFile(mainPath) // #nosec G304 -- mainPath is a package-owned constant (/etc/ssh/sshd_config), overridden only by tests
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", mainPath, err)
	}
	var dropIns []sshdpolicy.DropIn
	if entries, derr := os.ReadDir(dropInDir); derr == nil {
		for _, e := range entries {
			d, rerr := os.ReadFile(filepath.Join(dropInDir, filepath.Base(e.Name()))) // #nosec G304 -- dropInDir is a package-owned constant; name is Base()-bounded
			if rerr != nil {
				continue
			}
			dropIns = append(dropIns, sshdpolicy.DropIn{Name: e.Name(), Content: string(d)})
		}
	}
	return sshdpolicy.Merge(string(main), dropIns), nil
}

// sshdDirective is sshdpolicy.Directive (first occurrence wins, Match
// blocks ignored); kept as a local name so the check reads naturally.
func sshdDirective(config, key, def string) string {
	return sshdpolicy.Directive(config, key, def)
}

// --- unattended upgrades ---------------------------------------------

// unattendedUpgradesCheck reads APT's periodic config. Debian-family
// only; anywhere else this is an unknown rather than a failure, since
// the equivalent mechanism differs (dnf-automatic, etc.).
func unattendedUpgradesCheck(p posturePaths) Check {
	c := Check{Name: "unattended security upgrades enabled"}
	data, err := os.ReadFile(p.aptPeriodic) // #nosec G304 -- package-owned constant, overridden only by tests
	if err != nil {
		if os.IsNotExist(err) {
			c.Detail = "could not determine: " + p.aptPeriodic +
				" absent (not a Debian-family host, or unattended-upgrades never configured)"
			return c
		}
		c.Detail = fmt.Sprintf("could not determine: reading %s: %v", p.aptPeriodic, err)
		return c
	}
	if !aptPeriodicEnabled(string(data), "Unattended-Upgrade") {
		c.Detail = `APT::Periodic::Unattended-Upgrade is not "1"`
		return c
	}
	c.OK = true
	c.Detail = `APT::Periodic::Unattended-Upgrade "1"`
	return c
}

// aptPeriodicEnabled reports whether APT::Periodic::<key> is set to a
// non-zero value. APT treats "0" as off and any other integer as a day
// interval, so "7" is enabled-but-weekly, not disabled.
func aptPeriodicEnabled(config, key string) bool {
	want := "APT::Periodic::" + key
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.HasPrefix(line, want) {
			continue
		}
		open := strings.Index(line, `"`)
		if open < 0 {
			continue
		}
		close := strings.Index(line[open+1:], `"`)
		if close < 0 {
			continue
		}
		val := strings.TrimSpace(line[open+1 : open+1+close])
		return val != "" && val != "0"
	}
	return false
}

// --- metadata server reachability ------------------------------------

// metadataReachableCheck reports whether the cloud metadata endpoint is
// reachable from this host.
//
// #1103 asks for "IMDSv2 required", and that is not honestly checkable
// here: IMDSv2 is an AWS concept, and GCP's Metadata-Flavor header
// requirement is unconditional, so a check claiming to verify it would
// be theatre. What this issue actually cares about — given it frames
// IMDS deny as "implemented, not armed" — is whether the pivot from a
// popped workload to instance credentials is open at all. That is
// directly measurable, so it is what gets measured and what the check is
// named for.
//
// OK means UNREACHABLE. The desirable state is the connection failing.
func metadataReachableCheck(p posturePaths) Check {
	c := Check{Name: "cloud metadata endpoint blocked"}
	if p.metadataDialer == nil {
		c.Detail = "could not determine: no dialer configured"
		return c
	}
	if err := p.metadataDialer(); err != nil {
		c.OK = true
		c.Detail = "169.254.169.254:80 unreachable from the host (" + err.Error() + ")"
		return c
	}
	c.Detail = "169.254.169.254:80 is reachable from the host: a workload that escapes its container can reach instance credentials"
	return c
}

