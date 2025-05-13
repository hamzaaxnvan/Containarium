package sentinel

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeRecoveryProvider is a CloudProvider whose status and StartInstance
// behavior the test drives, counting StartInstance calls. Used to exercise
// the #514 periodic-recovery + backoff logic without a real cloud.
type fakeRecoveryProvider struct {
	status     InstanceStatus
	startErr   error // returned by StartInstance (nil = accepted)
	startCalls int
}

func (f *fakeRecoveryProvider) GetInstanceStatus(context.Context) (InstanceStatus, error) {
	return f.status, nil
}
func (f *fakeRecoveryProvider) GetInstanceIP(context.Context) (string, error) { return "10.0.0.1", nil }
func (f *fakeRecoveryProvider) StartInstance(context.Context) error {
	f.startCalls++
	return f.startErr
}

// newMaintenanceManager builds a Manager pinned in maintenance with a
// single provider-backed GCP backend, ready to exercise recovery.
func newMaintenanceManager(t *testing.T, p *fakeRecoveryProvider, cfg Config) (*Manager, *Backend) {
	t.Helper()
	m := NewManager(cfg, p)
	m.state.Store(StateMaintenance)
	b := &Backend{ID: "gcp", Type: BackendGCP, IP: "10.0.0.1", Provider: p, Healthy: false}
	m.backends.Add(b)
	return m, b
}

func TestRecovery_DefaultsApplied(t *testing.T) {
	m := NewManager(Config{}, &fakeRecoveryProvider{})
	if m.config.RecoveryBackoffInitial != 30*time.Second {
		t.Errorf("initial default = %v, want 30s", m.config.RecoveryBackoffInitial)
	}
	if m.config.RecoveryBackoffMax != 5*time.Minute {
		t.Errorf("max default = %v, want 5m", m.config.RecoveryBackoffMax)
	}
	// Max < Initial is clamped up to Initial.
	m2 := NewManager(Config{RecoveryBackoffInitial: time.Minute, RecoveryBackoffMax: time.Second}, &fakeRecoveryProvider{})
	if m2.config.RecoveryBackoffMax < m2.config.RecoveryBackoffInitial {
		t.Errorf("max (%v) should be clamped >= initial (%v)", m2.config.RecoveryBackoffMax, m2.config.RecoveryBackoffInitial)
	}
}
