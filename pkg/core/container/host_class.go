package container

import (
	"os/exec"
	"strings"
	"sync"
)

// Host-class detection for environment-specific code paths in
// jump_server.go and friends.
//
// Background: the useradd preconditions (`systemctl stop google-guest-
// agent`, force-removing `/etc/.pwd.lock`) were written for GCP VMs,
// where `google-guest-agent` races with local `useradd` over the
// passwd lock. On any non-GCP host (a VirtualBox lab spot, an on-prem
// box, AWS, Azure, …) those steps are at best no-ops with misleading
// "Access denied" stderr, and at worst actively harmful (force-removing
// a lock file held by something else). Issue #351.
//
// Hosts don't change class at runtime, so we cache the answer behind
// sync.Once. The detector is a package var so tests can swap in a
// deterministic stub without shelling out to `systemd-detect-virt`.

var (
	hostClassOnce   sync.Once
	hostClassResult hostClass

	// hostClassDetector returns the detected hostClass. Real impl
	// runs `systemd-detect-virt`; tests can replace.
	hostClassDetector func() hostClass = detectHostClassViaSystemd
)

// hostClass identifies the platform a host runs on, for the small
// number of places (useradd preconditions) that need to behave
// differently per-platform. Keep this enum tight — we don't want a
// proliferation of platform-specific branches.
type hostClass int

const (
	hostClassUnknown hostClass = iota
	hostClassGCP
)

// getHostClass returns the detected host class, computing it on the
// first call and caching for the lifetime of the process.
func getHostClass() hostClass {
	hostClassOnce.Do(func() {
		hostClassResult = hostClassDetector()
