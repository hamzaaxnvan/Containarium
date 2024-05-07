package security

import (
	"fmt"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Installing ClamAV in a tenant's box (#2631, #2659).
//
// The platform runs the package install INSIDE the box, in the box's own
// context, started by its owner. It does not read the box's files and keeps no
// package-manager output: the result is a typed outcome and a short fixed
// message.

// installTimeout bounds the package install and the first signature download.
const installTimeout = 15 * time.Minute

// installFreshclamTimeout bounds the first signature download on its own.
const installFreshclamTimeout = 10 * time.Minute

// Messages. Fixed strings only: nothing from the box's output is echoed back.
const (
	msgInstalled      = "ClamAV and its signatures are installed in the box"
	msgAlreadyPresent = "ClamAV and its signatures are already installed; nothing was run"
	msgNotRunning     = "the box is not running; start it and try again"
	msgFamily         = "this command installs ClamAV on Debian/Ubuntu boxes only; install ClamAV manually in this box"
	msgSizeFmt        = "the box's memory limit %s is below the %d GB ClamAV needs; resize it first"
	msgInstallFailed  = "the ClamAV package install failed in the box (no network egress to the package mirror, or apt is locked); retry, or install manually"
	msgSigFailed      = "ClamAV installed, but downloading signatures failed (the box needs outbound access to the ClamAV signature servers); run freshclam in the box once reachable"
	msgVerifyFailed   = "the install finished but ClamAV or its signatures are still missing in the box"
)

// debianFamilyProbe exits 0 on a Debian-family box (apt-get present, no
// /etc/redhat-release) and 1 otherwise, never 127 (see presenceProbe).
const debianFamilyProbe = "if command -v apt-get >/dev/null 2>&1 && [ ! -f /etc/redhat-release ]; then exit 0; else exit 1; fi"

// clamavPresent reports whether clamscan and a signature database exist in the
// box. err means the box could not be asked at all.
