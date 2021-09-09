package connectcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Key-propagation wait (#2013).
//
// Authorizing a key (or creating a box) updates the box's host-side
// authorized_keys at once, but the sentinel only mirrors it into sshpiper on
// its next keysync — up to a couple of minutes when the daemon's immediate
// resync notification is not wired or is lost. During that window ssh answers
// `Permission denied (publickey)` although nothing is wrong. KeyWait retries
// exactly that failure, for a bounded window, and only for an invocation that
// itself just created the box or authorized the key: an invocation that did
// neither has no reason to expect lag, so it fails once as before.

// DefaultKeyWaitWindow bounds the retry: comfortably over one keysync tick.
const DefaultKeyWaitWindow = 150 * time.Second

// DefaultKeyWaitInterval is the pause between attempts.
const DefaultKeyWaitInterval = 5 * time.Second

// KeyWaitEnv overrides the window when no flag is given ("0" disables).
const KeyWaitEnv = "CONTAINARIUM_KEY_WAIT"

// ErrKeyNotLearned is returned (wrapped) when the window ran out with ssh
// still answering publickey-denied.
var ErrKeyNotLearned = errors.New("the sentinel has not learned your key")

// SSHError is a failure of the local ssh client itself (exit 255), carrying
// what it printed on stderr so the failure can be classified.
type SSHError struct {
	ExitCode int
	Stderr   string
	// Err, when set, is the underlying process error; its message is kept
	// so a failure that is not retried reads exactly as it did before.
	Err error
}

func (e *SSHError) Unwrap() error { return e.Err }

func (e *SSHError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	msg := lastLine(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("ssh exited %d", e.ExitCode)
	}
	return fmt.Sprintf("ssh exited %d: %s", e.ExitCode, msg)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// IsPublickeyDenied reports whether err is ssh's own "Permission denied
// (publickey…)" — the server rejected every key offered. Only an *SSHError
// with ssh's exit code 255 counts: a remote command that merely printed the
// same words is not an auth failure.
func IsPublickeyDenied(err error) bool {
	var se *SSHError
	if !errors.As(err, &se) || se.ExitCode != 255 {
		return false
	}
	return strings.Contains(se.Stderr, "Permission denied (publickey")
}

// Clock is the time source KeyWait uses; injected so tests never sleep.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// KeyWait is the bounded retry of publickey denials.
