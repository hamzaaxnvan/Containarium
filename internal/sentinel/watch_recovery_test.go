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
