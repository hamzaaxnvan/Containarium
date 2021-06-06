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
