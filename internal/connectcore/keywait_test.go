package connectcore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeClock is an injected clock: Sleep advances Now instantly, so the retry
// loop's timing is exercised without a single real sleep.
type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

// fakeDialer returns the scripted results in order, then repeats the last one.
type fakeDialer struct {
	results  []error
	attempts int
}

func (f *fakeDialer) dial(_ context.Context, attempt int) error {
	f.attempts++
	if attempt != f.attempts {
		panic("attempt numbers must be 1-based and sequential")
	}
	i := attempt - 1
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i]
}

func denied() error {
	return &SSHError{ExitCode: 255, Stderr: "alice@ssh.example.com: Permission denied (publickey).\n"}
}

func TestKeyWaitDo(t *testing.T) {
	hostKey := &SSHError{ExitCode: 255, Stderr: "Host key verification failed.\n"}
	refused := &SSHError{ExitCode: 255, Stderr: "ssh: connect to host ssh.example.com port 22: Connection refused\n"}
	timeout := &SSHError{ExitCode: 255, Stderr: "ssh: connect to host ssh.example.com port 22: Operation timed out\n"}

	tests := []struct {
		name         string
		armed        bool
		window       time.Duration
		results      []error
		wantAttempts int
		wantErr      bool
		wantErrIs    error // nil = don't check
		wantErrText  []string
		wantStatus   bool // did the waiting line get printed?
	}{
		{
			name:         "first attempt succeeds: no output",
			armed:        true,
			window:       150 * time.Second,
			results:      []error{nil},
			wantAttempts: 1,
