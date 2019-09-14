package nodevm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// guestBinaryPath is where the containarium binary lands inside the node VM.
// #1781: a node VM runs the full daemon build (it becomes a pool backend),
// so it gets the containariumd name like any other host-side install.
const guestBinaryPath = "/usr/local/bin/containariumd"

// guestTokenPath is the perm-tight file the in-guest tunnel reads its
// token from (never passed on argv / kernel cmdline).
const guestTokenPath = "/etc/containarium/tunnel.token" // #nosec G101 -- filesystem path to the token file, not a credential value

// Manager orchestrates node-VM lifecycle over the incus CLI.
type Manager struct {
	run Runner
	// BinaryPath is the local containarium binary pushed into each node VM
	// (so the node matches the operator's version exactly).
	BinaryPath string
	// waitAttempts / waitSleep bound the agent-readiness poll. A VM takes
	// tens of seconds to boot its incus-agent, so we must sleep between
	// probes — without it the retries burn out in milliseconds. Small/zero
	// in tests (success on the first probe never reaches the sleep).
	waitAttempts int
	waitSleep    time.Duration
}

// NewManager builds a Manager. binaryPath is the local containarium binary
// to push into node VMs (empty = skip the push, e.g. for a dry run).
func NewManager(r Runner, binaryPath string) *Manager {
	return &Manager{run: r, BinaryPath: binaryPath, waitAttempts: 60, waitSleep: 4 * time.Second}
}

// Node is one node VM as listed on the host.
