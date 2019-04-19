// Package sshconfig generates a self-contained OpenSSH config file for
// every container the user can reach. The file is meant to be Include'd
// from ~/.ssh/config — the user adds one line once, and we never touch
// their primary config:
//
//	Include ~/.containarium/ssh_config
//
// After that, `ssh <container-name>` and `scp <container-name>:...` work
// without any further setup.
package sshconfig

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/footprintai/containarium/internal/hostport"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// Options controls how Host blocks are rendered:
//
//  1. Direct (Sentinel == ""): user is on the same network as the
//     container; the Host block uses the container's IP directly. This
//     is the local-Incus / dev workstation case. For a remote single VM,
//     JumpHost adds a ProxyJump through the host's per-user account.
//  2. Via sentinel: every connection goes to the sentinel's SSH port
//     and sshpiper routes by username to the right backend. The Host
//     block sets HostName=<sentinel> and User=<container-name>.
type Options struct {
	// Sentinel is the public-facing SSH endpoint, e.g.
	// "<cluster>.example.com" or "sentinel.example.com:22". Empty
	// means generate direct entries against each container's IP.
	Sentinel string
	// JumpHost is the VM's SSH endpoint (host or host:port). In direct
	// mode, boxes without a daemon-reported SSHHost use it as a jump
	// server, with the daemon-reported Username as the jump account.
	JumpHost string
	// SentinelPort is the SSH port on the sentinel. Default 22.
	SentinelPort int
	// IdentityFile, if non-empty, is rendered as IdentityFile in every
	// Host block. Leave empty to let the user manage keys via the
	// agent / global config.
	IdentityFile string
	// User overrides the per-Host User. If empty: when Sentinel is set,
	// User=container-name (sshpiper routes); otherwise User="ubuntu".
	User string
	// IncludeStopped emits Host blocks for stopped containers too. Off
	// by default — if the container can't accept connections, an entry
	// just produces confusing timeouts.
	IncludeStopped bool
}

// Generated is the rendered config plus metadata for the caller (the CLI
// uses Count / SkippedNoAddr in user-facing output).
type Generated struct {
	Content        string
	Count          int
	SkippedNoAddr  int
	SkippedStopped int
}

// Header / footer markers delimit the section we own. If anyone ever
// needs to merge into an existing file, scanning between these is the
// supported way to find our block. Keep them stable.
const (
	beginMarker = "# >>> containarium ssh-config (managed) >>>"
	endMarker   = "# <<< containarium ssh-config (managed) <<<"
)

// Generate renders a self-contained ssh_config from the given containers.
// The output always includes the begin/end markers so callers writing
// into a larger file can locate and replace just our block.
func Generate(containers []incus.ContainerInfo, opts Options) Generated {
	if opts.SentinelPort == 0 {
		opts.SentinelPort = 22
	}

	var b strings.Builder
