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
type Node struct {
	Name  string
	State string
}

// Provision creates the VM, (for GPU) attaches the card, pushes the
// binary + token, and runs the in-guest bootstrap. Idempotent intent:
// if the VM already exists it reconciles the bootstrap rather than
// failing (full reconcile of partial state is a follow-up).
func (m *Manager) Provision(s Spec) (*Node, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}

	exists, err := m.vmExists(s.Name)
	if err != nil {
		return nil, err
	}
	if !exists {
		if _, err := m.run.Run(launchArgs(s)...); err != nil {
			return nil, fmt.Errorf("launch VM: %w", err)
		}
		if s.Kind == KindGPU {
			if _, err := m.run.Run(gpuDeviceArgs(s.Name, s.GPUPCI)...); err != nil {
				return nil, fmt.Errorf("attach GPU: %w", err)
			}
		}
	}

	if err := m.waitAgent(s.Name); err != nil {
		return nil, err
	}

	// Deliver the binary (so the node matches the operator's version).
	if m.BinaryPath != "" {
		if _, err := m.run.Run("file", "push", m.BinaryPath, s.Name+guestBinaryPath, "--mode", "0755"); err != nil {
			return nil, fmt.Errorf("push binary: %w", err)
		}
	}

	// Deliver the tunnel token as a 0600 file — never on argv.
	if s.TunnelToken != "" {
		if err := m.pushToken(s.Name, s.TunnelToken); err != nil {
			return nil, err
		}
	}

	// Run the in-guest bootstrap (nested Incus + daemon + tunnel).
	script := RenderBootstrap(s, guestTokenPath)
	if _, err := m.run.Run("exec", s.Name, "--", "bash", "-c", script); err != nil {
		return nil, fmt.Errorf("bootstrap guest: %w", err)
	}

	return &Node{Name: s.Name, State: "provisioned"}, nil
}

// pushToken writes the token to a local 0600 temp file and `incus file
// push`es it to the guest at 0600, then removes the temp. Keeps the token
// off every command line.
func (m *Manager) pushToken(name, token string) error {
	tmp, err := os.CreateTemp("", "ctnr-tunnel-token-")
	if err != nil {
		return fmt.Errorf("stage token: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("chmod token tmp: %w", err)
	}
	if _, err := tmp.WriteString(token); err != nil {
		return fmt.Errorf("write token tmp: %w", err)
	}
	_ = tmp.Close()
	// Ensure the parent dir exists in the guest, then push 0600.
	if _, err := m.run.Run("exec", name, "--", "mkdir", "-p", filepath.Dir(guestTokenPath)); err != nil {
		return fmt.Errorf("mkdir token dir: %w", err)
	}
	if _, err := m.run.Run("file", "push", tmp.Name(), name+guestTokenPath, "--mode", "0600"); err != nil {
		return fmt.Errorf("push token: %w", err)
	}
	return nil
}

func (m *Manager) waitAgent(name string) error {
