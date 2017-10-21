package incus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// waitWithTimeout bounds an Operation.WaitContext-shaped call (cloud#1128):
// op.Wait() has no deadline of its own, so a wedged Incus operation — same
// class of liblxc-socket flakiness this file already retries around for
// GetInstanceState (isTransientStateErr, citing OSS #931) — blocks its
// caller forever instead of surfacing an error. The Incus SDK's
// Operation.WaitContext performs a genuine `select` on ctx.Done() (verified
// against github.com/lxc/incus/v7/client), so a deadline-bound context
// actually interrupts a wedged wait; wait below simulates exactly that
// select by blocking on ctx.Done() itself, mirroring what the real SDK does
// when the underlying operation never completes.

func TestWaitWithTimeout_WedgedOperationTimesOutRatherThanHangingForever(t *testing.T) {
	wedged := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	start := time.Now()
	err := waitWithTimeout(20*time.Millisecond, wedged)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("waitWithTimeout blocked for %s — not bounded by the timeout", elapsed)
	}
	if err == nil {
		t.Fatal("wedged operation must return an error, not nil")
	}
	if !strings.Contains(err.Error(), "wedged") {
		t.Errorf("error should say the operation looks wedged, not just cancelled: %q", err.Error())
	}
