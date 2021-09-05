// Package sshdpolicy is the single source of truth for the SSH-server policy
// Containarium applies inside every box (#2424): the managed drop-in that
// turns password authentication off, and the pure evaluation of an sshd
// configuration against that policy.
//
// It is deliberately IO-free. Box provisioning (pkg/core/container) writes
// DropInContent through the incus file API; the host posture check
// (internal/hostcheck) and the box posture reconciler (internal/security)
// each gather sshd_config + drop-ins their own way and hand the pieces to
// Merge/Evaluate. One parser means the host check and the box check cannot
// disagree about what "password auth is off" means.
//
// Why the box side matters even though the public SSH ingress never reaches
// a box sshd (the sentinel pipes to the backend host sshd, then
// containarium-shell → incus exec): a box owner has NOPASSWD sudo, so one
// `sudo passwd` unlocks password login on a box whose sshd inherited the
// image default `PasswordAuthentication yes`. That sshd is reachable from
// the tenant's own boxes, from the LAN on direct in-network backends, and —
// via any reverse tunnel the owner cares to run — from the internet. The
// drop-in closes that for the stock sshd; the reconciler re-asserts it and
// records tampering as a finding.
package sshdpolicy

import (
	"sort"
	"strconv"
	"strings"
)

const (
	// MainConfigPath is sshd's main configuration file.
	MainConfigPath = "/etc/ssh/sshd_config"
	// DropInDir is the Include directory modern distros wire at the TOP of
	// sshd_config, so its files take precedence under first-match-wins.
	DropInDir = "/etc/ssh/sshd_config.d"
	// DropInName sorts before every distro/cloud-init drop-in (50-*.conf),
	// which is what makes it win.
	DropInName = "00-containarium.conf"
	// DropInPath is DropInDir/DropInName.
	DropInPath = DropInDir + "/" + DropInName
	// DropInMode is the file mode the drop-in is written with.
	DropInMode = "0644"

	// MarkerKey is the incus instance config key the posture reconciler
	// sets once a box has been brought under policy. It lives on the host
	// side of the box boundary — a tenant cannot touch it — which is what
	// lets the reconciler tell "never applied yet (backfill, silent)" from
	// "applied before and now gone (tampering, finding)".
	MarkerKey = "user.containarium.sshd_policy"
	// MarkerValue is bumped when the drop-in's meaning changes.
	MarkerValue = "v1"
)

// dropInContent is what DropInContent returns. KbdInteractiveAuthentication
// closes the PAM password prompt that `PasswordAuthentication no` alone
// leaves open; PermitEmptyPasswords is belt-and-braces for an account whose
// password was set to "". The header is the only tenant-facing notice that
// edits do not stick.
const dropInContent = `# Managed by Containarium (#2424). Do not edit: the platform re-asserts this
# file and records changes as a security finding. SSH to a box is key-only.
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitEmptyPasswords no
`

// DropInContent returns the managed drop-in as bytes.
func DropInContent() []byte { return []byte(dropInContent) }

// DropIn is one file from DropInDir.
