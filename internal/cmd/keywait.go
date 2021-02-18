package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/footprintai/containarium/internal/connectcore"
)

// Key-propagation wait for the CLI's ssh paths (#2013): `connect`, `code *`
// and `quickstart`. The retry policy itself lives in connectcore.KeyWait;
// this file adapts it to the local ssh binary.

const keyWaitFlagUsage = "how long to keep retrying `Permission denied (publickey)` right after this command authorized your key or created the box, while the sentinel learns the key (e.g. 150s, 3m; 0 disables; default 150s, or $" + connectcore.KeyWaitEnv + ")"

// keyWaitClock is a test seam; nil means the wall clock.
var keyWaitClock connectcore.Clock

// newKeyWait builds the retry policy for one invocation. armed must be true
// only when this invocation itself created the box or authorized the key.
func newKeyWait(flag string, armed bool, status io.Writer) (connectcore.KeyWait, error) {
	window, err := connectcore.ResolveKeyWaitWindow(flag, os.Getenv(connectcore.KeyWaitEnv))
	if err != nil {
		return connectcore.KeyWait{}, err
	}
	return connectcore.KeyWait{Armed: armed, Window: window, Status: status, Clock: keyWaitClock}, nil
}

// remoteExitError is a non-zero exit that is NOT the ssh client failing to
// get in: the remote command's own exit code, to be propagated verbatim.
type remoteExitError struct {
	code int
	err  error // the process error, kept for its message; may be nil
}

func (e *remoteExitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("remote command exited %d", e.code)
}

func (e *remoteExitError) Unwrap() error { return e.err }

// probeSSHArgs turns an ssh argv (options + destination, no remote command)
// into a silent auth check: BatchMode so nothing prompts, a connect timeout
// so a dead host is a fast non-publickey failure, and `true` as the command.
func probeSSHArgs(base []string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15"}
	args = append(args, base...)
	return append(args, "true")
}

// sshProbeFn runs a probe argv (from probeSSHArgs) with stderr captured.
// Test seam.
var sshProbeFn = runSSHProbe

func runSSHProbe(ctx context.Context, args []string) error {
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}
	// #nosec G204 -- sshBin is the resolved `ssh` binary; args are the
	// same daemon-resolved target the real invocation uses, plus fixed
	// options and the fixed remote command `true`.
	c := exec.CommandContext(ctx, sshBin, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	return classifySSHRun(c.Run(), stderr.String())
}
