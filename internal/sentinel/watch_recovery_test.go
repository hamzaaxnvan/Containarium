package sentinel

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWatchOnly_AddedBackendNeverEntersProxyPool(t *testing.T) {
	m := NewManager(Config{}, &fakeRecoveryProvider{status: StatusRunning})
	p := &fakeRecoveryProvider{status: StatusRunning}
	m.AddWatchOnlyBackend("other-host", p)

	if m.backends.Count() != 0 {
		t.Fatalf("watch-only backend must not be added to the HTTP proxy pool; backends.Count() = %d", m.backends.Count())
	}
	if got := m.backends.Get("other-host"); got != nil {
		t.Fatalf("watch-only backend must not be reachable via backends.Get; got %+v", got)
	}
	if len(m.watchOnly) != 1 || m.watchOnly[0].id != "other-host" {
		t.Fatalf("expected exactly one watch-only entry \"other-host\", got %+v", m.watchOnly)
	}
}

func TestWatchOnly_StoppedTargetGetsStarted(t *testing.T) {
	p := &fakeRecoveryProvider{status: StatusStopped}
	m := NewManager(Config{RecoveryBackoffInitial: 30 * time.Second, RecoveryBackoffMax: 5 * time.Minute}, &fakeRecoveryProvider{})
	m.AddWatchOnlyBackend("ase1-prod", p)
	wb := m.watchOnly[0]

	m.diagnoseAndRecoverWatchOnly(context.Background(), wb)

	if p.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", p.startCalls)
	}
	if !wb.down {
		t.Fatal("wb.down should be true after observing StatusStopped")
	}
}

func TestWatchOnly_FailedStartGrowsBackoffIndependentlyOfPrimary(t *testing.T) {
	primary := &fakeRecoveryProvider{status: StatusTerminated, startErr: errors.New("primary down too")}
	watched := &fakeRecoveryProvider{status: StatusTerminated, startErr: errors.New("no spot capacity")}
	cfg := Config{RecoveryBackoffInitial: 30 * time.Second, RecoveryBackoffMax: 5 * time.Minute}

	m, primaryBackend := newMaintenanceManager(t, primary, cfg)
	m.AddWatchOnlyBackend("ase1-prod", watched)
	wb := m.watchOnly[0]
	ctx := context.Background()

	// Drive the PRIMARY backend's recovery once — must not perturb the
	// watch-only target's independent timeline.
	m.diagnoseAndRecover(ctx, primaryBackend)
	if wb.recoveryBackoff != 0 {
		t.Fatalf("watch-only backoff should be untouched by primary recovery; got %v", wb.recoveryBackoff)
	}

	m.diagnoseAndRecoverWatchOnly(ctx, wb)
	if watched.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", watched.startCalls)
	}
	if wb.recoveryBackoff != 30*time.Second {
		t.Fatalf("backoff after first failure = %v, want 30s", wb.recoveryBackoff)
	}
	// The primary's own backoff must likewise be unaffected by the
	// watch-only attempt.
	if m.recoveryBackoff != 30*time.Second {
		t.Fatalf("primary backoff should reflect only its own failure; got %v", m.recoveryBackoff)
	}

	// Before the window elapses, a retry must not fire again.
	m.maybeRetryWatchOnlyRecovery(ctx, wb)
	if watched.startCalls != 1 {
		t.Fatalf("re-attempt before backoff window: startCalls = %d, want still 1", watched.startCalls)
	}

	// Force the window open → re-attempts and doubles the backoff.
	wb.nextRecoveryAttempt = time.Now().Add(-time.Second)
	m.maybeRetryWatchOnlyRecovery(ctx, wb)
	if watched.startCalls != 2 {
		t.Fatalf("after window elapsed, startCalls = %d, want 2", watched.startCalls)
	}
	if wb.recoveryBackoff != 60*time.Second {
		t.Fatalf("backoff after second failure = %v, want 60s (doubled)", wb.recoveryBackoff)
	}
}

func TestWatchOnly_RecoversOnceRunning(t *testing.T) {
	p := &fakeRecoveryProvider{status: StatusTerminated, startErr: errors.New("x")}
	m := NewManager(Config{RecoveryBackoffInitial: 30 * time.Second, RecoveryBackoffMax: 5 * time.Minute}, &fakeRecoveryProvider{})
	m.AddWatchOnlyBackend("ase1-prod", p)
	wb := m.watchOnly[0]
	ctx := context.Background()

	m.diagnoseAndRecoverWatchOnly(ctx, wb)
	if !wb.down || wb.recoveryBackoff == 0 {
		t.Fatal("precondition: target should be marked down with a backoff scheduled")
	}

	p.status = StatusRunning
	m.diagnoseAndRecoverWatchOnly(ctx, wb)

	if wb.down {
		t.Fatal("wb.down should be false once StatusRunning is observed")
