package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/connectcore"
	"github.com/spf13/cobra"
)

// stepClock is an injected clock for the cmd-level key-wait tests: Sleep
// advances Now instantly, so no test really sleeps.
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }
func (c *stepClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

func pkDenied() error {
	return &connectcore.SSHError{ExitCode: 255, Stderr: "Permission denied (publickey).\n"}
}

// scripted returns results in order, repeating the last.
func scripted(results ...error) (func() error, *int) {
	n := 0
	return func() error {
		i := n
		if i >= len(results) {
			i = len(results) - 1
		}
		n++
		return results[i]
	}, &n
}

func withProbe(t *testing.T, probe func(context.Context, []string) error) {
	t.Helper()
	orig := sshProbeFn
	t.Cleanup(func() { sshProbeFn = orig })
	sshProbeFn = probe
}

func TestSSHWithKeyWait(t *testing.T) {
	tests := []struct {
		name        string
		armed       bool
		ops         []error // real ssh invocation results
		probes      []error // auth-probe results
		wantOps     int
		wantErr     bool
		wantErrIs   error
		wantRemote  bool // error is the pass-through remote exit
		wantWaiting bool
	}{
		{
			name:    "first attempt succeeds: no probe, no output",
			armed:   true,
			ops:     []error{nil},
			probes:  []error{errors.New("probe must not run")},
			wantOps: 1,
		},
		{
			name:  "lag: denied, probe denied twice, then connects",
			armed: true,
			ops:   []error{pkDenied(), nil},
			// confirm (denied), attempt-2 probe (denied), attempt-3 probe (ok)
			probes:      []error{pkDenied(), pkDenied(), nil},
			wantOps:     2,
			wantWaiting: true,
		},
		{
			name:        "never learned: names the window",
			armed:       true,
			ops:         []error{pkDenied()},
			probes:      []error{pkDenied()},
			wantOps:     1,
			wantErr:     true,
			wantErrIs:   connectcore.ErrKeyNotLearned,
			wantWaiting: true,
		},
		{
			name:       "remote command printed the words and exited 255: our auth works, not retried",
			armed:      true,
			ops:        []error{pkDenied(), nil},
			probes:     []error{nil},
			wantOps:    1,
			wantErr:    true,
			wantRemote: true,
		},
		{
			name:    "host key failure: not retried",
			armed:   true,
			ops:     []error{&connectcore.SSHError{ExitCode: 255, Stderr: "Host key verification failed.\n"}, nil},
